package localfs

import (
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"hash/crc32"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

type output struct {
	s                  *Registry
	root, temp, target string
	file               *os.File
	committed          bool
}

func (s *Registry) createOutput(ctx context.Context, d configuration.Directory, name string, save ...func(json.RawMessage) error) (*output, error) {
	var n [16]byte
	if _, e := rand.Read(n[:]); e != nil {
		return nil, e
	}
	temp := path.Join(d.Directory, ".http-tool-"+hex.EncodeToString(n[:]))
	if len(save) > 0 && save[0] != nil {
		cp, _ := json.Marshal(map[string]any{"Phase": "allocating", "RootID": d.RootID, "Temp": temp, "Target": path.Join(d.Directory, name)})
		if e := save[0](cp); e != nil {
			return nil, e
		}
	}
	f, e := s.OpenFile(ctx, d.RootID, temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, localError(e)
	}
	return &output{s: s, root: d.RootID, temp: temp, target: path.Join(d.Directory, name), file: f}, nil
}
func (o *output) Abort() {
	o.file.Close()
	if !o.committed {
		_ = o.s.RemoveFile(context.Background(), o.root, o.temp)
	}
}
func (o *output) Commit(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := o.file.Sync(); e != nil {
		return localError(e)
	}
	if e := o.s.CommitFile(ctx, o.root, o.temp, o.target, o.file); e != nil {
		target, openErr := o.s.OpenFile(context.Background(), o.root, o.target, os.O_RDONLY, 0)
		if openErr == nil {
			a, ea := target.Stat()
			b, eb := o.file.Stat()
			target.Close()
			if ea == nil && eb == nil && os.SameFile(a, b) {
				o.committed = true
				return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "输出已发布但目录提交未确认")
			}
		}
		return localError(e)
	}
	o.committed = true
	return nil
}
func localError(e error) error {
	if os.IsExist(e) {
		return fault.New(fault.Conflict, "TARGET_EXISTS", "目标已存在，未覆盖")
	}
	return fault.New(fault.Unavailable, "LOCAL_IO_ERROR", "本地读写失败，可能空间不足或权限不可用")
}
func (s *Registry) Checksum(ctx context.Context, f tool.LocalFile, algorithms []string) (tool.Checksum, error) {
	var v tool.Checksum
	in, e := s.OpenFile(ctx, f.RootID, f.Path, os.O_RDONLY, 0)
	if e != nil {
		return v, fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地文件不可读取")
	}
	defer in.Close()
	before, e := in.Stat()
	if e != nil {
		return v, e
	}
	md, sl, crc := md5.New(), md5.New(), crc32.NewIEEE()
	block := md5.New()
	blocks := []string{}
	var n, blockN int64
	buf := make([]byte, 64<<10)
	for {
		if e := ctx.Err(); e != nil {
			return v, e
		}
		got, err := in.Read(buf)
		b := buf[:got]
		if got > 0 {
			md.Write(b)
			crc.Write(b)
			if n < 262144 {
				sl.Write(b[:min(int64(got), 262144-n)])
			}
			block.Write(b)
			blockN += int64(got)
			n += int64(got)
			if blockN == 4<<20 {
				blocks = append(blocks, hex.EncodeToString(block.Sum(nil)))
				block.Reset()
				blockN = 0
				if len(blocks) > 100000 {
					return v, fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "本地块摘要过多")
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return v, err
		}
	}
	if blockN > 0 || n == 0 {
		blocks = append(blocks, hex.EncodeToString(block.Sum(nil)))
	}
	after, e := in.Stat()
	if e != nil {
		return v, e
	}
	if n != before.Size() || n != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return v, fault.New(fault.Conflict, "LOCAL_FILE_CHANGED", "计算摘要时源文件发生变化")
	}
	v = tool.Checksum{Size: strconv.FormatInt(n, 10), Status: "missing"}
	for _, a := range algorithms {
		switch a {
		case "md5":
			x := hex.EncodeToString(md.Sum(nil))
			v.MD5 = &x
			v.Status = "verified"
		case "slice_md5":
			x := hex.EncodeToString(sl.Sum(nil))
			v.SliceMD5 = &x
		case "crc32":
			x := strconv.FormatUint(uint64(crc.Sum32()), 10)
			v.CRC32 = &x
		case "block_md5":
			v.Blocks = blocks
		}
	}
	return v, nil
}
func (s *Registry) Transform(ctx context.Context, src tool.LocalFile, dst configuration.Directory, method string, key []byte, zipped, remove, encrypt bool, save func(json.RawMessage) error) (int64, error) {
	parts := strings.Split(method, "-")
	if len(parts) != 3 {
		return 0, fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "不支持的加密算法")
	}
	bits, e := strconv.Atoi(parts[1])
	if e != nil || bits != 128 && bits != 192 && bits != 256 {
		return 0, fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "不支持的密钥长度")
	}
	material := make([]byte, bits/8)
	copy(material, key)
	defer func() {
		for i := range material {
			material[i] = 0
		}
	}()
	block, e := aes.NewCipher(material)
	if e != nil {
		return 0, e
	}
	in, e := s.OpenFile(ctx, src.RootID, src.Path, os.O_RDONLY, 0)
	if e != nil {
		return 0, fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "源文件不可读取")
	}
	defer in.Close()
	before, e := in.Stat()
	if e != nil {
		return 0, e
	}
	name := path.Base(src.Path) + ".encrypt"
	if !encrypt {
		name = strings.TrimSuffix(path.Base(src.Path), ".encrypt")
		if name == path.Base(src.Path) {
			name += ".decrypted"
		}
	}
	out, e := s.createOutput(ctx, dst, name, save)
	if e != nil {
		return 0, e
	}
	defer out.Abort()
	persist := func(phase string) error {
		cp, _ := json.Marshal(map[string]any{"Phase": phase, "RootID": out.root, "Temp": out.temp, "Target": out.target})
		return save(cp)
	}

	iv := make([]byte, aes.BlockSize)
	if encrypt {
		if _, e = rand.Read(iv); e != nil {
			return 0, e
		}
		if _, e = out.file.Write(iv); e != nil {
			return 0, localError(e)
		}
	} else {
		if _, e = io.ReadFull(contextReader{ctx, in}, iv); e != nil {
			return 0, fault.New(fault.Invalid, "INVALID_CIPHERTEXT", "密文缺少完整IV")
		}
	}
	var stream cipher.Stream
	switch parts[2] {
	case "ctr":
		stream = cipher.NewCTR(block, iv)
	case "cfb":
		if encrypt {
			stream = cipher.NewCFBEncrypter(block, iv)
		} else {
			stream = cipher.NewCFBDecrypter(block, iv)
		}
	case "ofb":
		stream = cipher.NewOFB(block, iv)
	default:
		return 0, fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "不支持的加密模式")
	}
	var n int64
	if encrypt {
		writer := &cipher.StreamWriter{S: stream, W: out.file}
		var w io.Writer = writer
		var gz *gzip.Writer
		if zipped {
			gz = gzip.NewWriter(writer)
			w = gz
		}
		n, e = s.copyTool(w, contextReader{ctx, in})
		if gz != nil {
			e = errors.Join(e, gz.Close())
		}
	} else {
		var reader io.Reader = &cipher.StreamReader{S: stream, R: contextReader{ctx, in}}
		var gz *gzip.Reader
		if zipped {
			gz, e = gzip.NewReader(reader)
			if e != nil {
				return 0, fault.New(fault.Invalid, "INVALID_CIPHERTEXT", "密钥错误或gzip密文损坏")
			}
			defer gz.Close()
			reader = gz
		}
		n, e = s.copyTool(out.file, contextReader{ctx, reader})
		if zipped && e != nil && ctx.Err() == nil {
			return 0, fault.New(fault.Invalid, "INVALID_CIPHERTEXT", "密钥错误或gzip校验失败")
		}
	}
	if e != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, localError(e)
	}
	after, e := in.Stat()
	if e != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return 0, fault.New(fault.Conflict, "LOCAL_FILE_CHANGED", "加解密过程中源文件发生变化")
	}
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if e := persist("committing"); e != nil {
		return 0, e
	}
	if e := out.Commit(ctx); e != nil {
		if fault.As(e).Code == "LOCAL_RESULT_UNKNOWN" {
			return n, e
		}
		_ = persist("prepared")
		return 0, e
	}
	// Record the committed output before optional source removal. Any lost receipt
	// retains the source, and the executor reports an uncertain local result.
	if e := persist("done"); e != nil {
		return n, fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "本地输出已提交但回执未确认，保留源文件")
	}
	if remove {
		if e := s.RemoveOpenedFile(ctx, src.RootID, src.Path, in); e != nil {
			return n, fault.New(fault.Conflict, "SOURCE_REMOVAL_FAILED", "输出已提交，源文件未删除")
		}
	}
	if e := persist("prepared"); e != nil {
		return n, fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "本地输出已提交但回执未确认")
	}
	return n, nil
}

func (o *output) Write(b []byte) (int, error) { return o.file.Write(b) }
func (s *Registry) CreateToolOutput(ctx context.Context, d configuration.Directory, name string, save func(json.RawMessage) error) (ports.ToolOutput, error) {
	return s.createOutput(ctx, d, name, save)
}
func (s *Registry) PrepareTool(ctx context.Context, p tool.Plan) error {
	var opts struct {
		Remove bool `json:"remove_source"`
	}
	_ = json.Unmarshal(p.Options, &opts)

	for _, u := range p.Units {
		if u.Local.RootID != "" {
			if opts.Remove {
				if _, e := s.File(ctx, u.Local.RootID, u.Local.Path, true); e != nil {
					return fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "删除源文件需要可写本地根目录")
				}
			}
			f, e := s.OpenFile(ctx, u.Local.RootID, u.Local.Path, os.O_RDONLY, 0)
			if e != nil {
				return fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地源文件不可读取")
			}
			f.Close()
		}
	}
	if p.Destination != nil {
		return s.MkdirAll(ctx, p.Destination.RootID, p.Destination.Directory)
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}

func (s *Registry) copyTool(w io.Writer, r io.Reader) (int64, error) {
	if s.toolCopy != nil {
		return s.toolCopy(w, r)
	}
	return io.CopyBuffer(w, r, make([]byte, 64<<10))
}

func (s *Registry) CleanupTool(ctx context.Context, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var cp struct{ RootID, Temp, Target string }
	if e := json.Unmarshal(raw, &cp); e != nil {
		return e
	}
	if cp.Temp == "" {
		return nil
	}
	if !strings.HasPrefix(path.Base(cp.Temp), ".http-tool-") || path.Dir(cp.Temp) != path.Dir(cp.Target) {
		return fault.New(fault.Invalid, "CHECKPOINT_INVALID", "本地工具临时引用无效")
	}
	return s.RemoveFile(ctx, cp.RootID, cp.Temp)
}
