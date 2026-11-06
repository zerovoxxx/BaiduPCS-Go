package bootstrap

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type writeFile struct {
	ID   int64
	Path string
	Body []byte
	Dir  bool
}
type writeUpstream struct {
	mu               sync.Mutex
	files            map[string]writeFile
	recycled         map[string]writeFile
	shares           map[string]map[string]any
	offline          map[string]map[string]string
	next             int64
	drop             string
	calls            map[string]int
	externalRequests int
	transferIDs      []int64
	period           int
	blockCopies      bool
	copyEntered      chan string
	copyRelease      chan struct{}
}

func newWriteUpstream() *writeUpstream {
	return &writeUpstream{files: map[string]writeFile{}, recycled: map[string]writeFile{}, shares: map[string]map[string]any{}, offline: map[string]map[string]string{}, next: 9007199254740993, copyEntered: make(chan string, 2), copyRelease: make(chan struct{}), calls: map[string]int{}}
}
func writeDigest(b []byte) string { sum := md5.Sum(b); return hex.EncodeToString(sum[:]) }
func (u *writeUpstream) seed(owner, p string, body string, dir bool) int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.next++
	u.files[owner+"\x00"+p] = writeFile{ID: u.next, Path: p, Body: []byte(body), Dir: dir}
	return u.next
}
func (u *writeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("BDUSS")
	if err != nil {
		http.Error(w, "cookie required", 401)
		return
	}
	owner := cookie.Value
	key := func(p string) string { return owner + "\x00" + p }
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/") {
		r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
	} else {
		r.ParseForm()
	}
	op := r.URL.Query().Get("method")
	if op == "" {
		op = strings.TrimPrefix(r.URL.Path, "/api/")
		op = strings.TrimPrefix(op, "/")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls[op]++
	respond := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	finish := func() {
		if u.drop == op {
			u.drop = ""
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]any{"errno": 0, "error_code": 0})
	}
	entry := func(v writeFile) any {
		return map[string]any{"fs_id": v.ID, "path": v.Path, "server_filename": path.Base(v.Path), "size": len(v.Body), "isdir": map[bool]int{true: 1, false: 0}[v.Dir], "md5": writeDigest(v.Body), "mtime": 1700000000}
	}
	var param struct {
		List []struct {
			Path string `json:"path"`
			From string `json:"from"`
			To   string `json:"to"`
			ID   int64  `json:"fs_id"`
		} `json:"list"`
	}
	_ = json.Unmarshal([]byte(r.Form.Get("param")), &param)
	switch op {
	case "user/getinfo":
		uk := 123
		if owner == "ZERO_OWNER" {
			uk = 0
		}
		if owner == "DIFFERENT_OWNER" {
			uk = 456
		}
		respond(map[string]any{"errno": 0, "records": []any{map[string]any{"uk": uk, "uname": "fixture"}}})
		return
	case "meta":
		if len(param.List) != 1 {
			http.Error(w, "meta shape", 400)
			return
		}
		v, ok := u.files[key(param.List[0].Path)]
		if !ok {
			respond(map[string]any{"error_code": 31066})
			return
		}
		respond(map[string]any{"list": []any{entry(v)}})
	case "list":
		values := []any{}
		keys := []string{}
		dir := r.URL.Query().Get("dir")
		for k, v := range u.files {
			if strings.HasPrefix(k, owner+"\x00") && path.Dir(v.Path) == dir {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		num, _ := strconv.Atoi(r.URL.Query().Get("num"))
		if num < 1 {
			num = 1000
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		for i := (page - 1) * num; i < min(page*num, len(keys)); i++ {
			values = append(values, entry(u.files[keys[i]]))
		}
		respond(map[string]any{"errno": 0, "list": values})
	case "mkdir":
		target := r.URL.Query().Get("path")
		u.next++
		u.files[key(target)] = writeFile{ID: u.next, Path: target, Dir: true}
		finish()
	case "copy", "move":
		if len(param.List) != 1 {
			http.Error(w, "write shape", 400)
			return
		}
		item := param.List[0]
		v, ok := u.files[key(item.From)]
		if !ok {
			respond(map[string]any{"error_code": 31066})
			return
		}
		if _, exists := u.files[key(item.To)]; exists && r.URL.Query().Get("ondup") != "overwrite" {
			respond(map[string]any{"error_code": 31061})
			return
		}
		if op == "copy" {
			u.next++
			v.ID = u.next
		} else {
			delete(u.files, key(item.From))
		}
		v.Path = item.To
		u.files[key(item.To)] = v
		if u.blockCopies && strings.Contains(item.To, "blocking") {
			u.copyEntered <- item.To
			u.mu.Unlock()
			select {
			case <-r.Context().Done():
			case <-u.copyRelease:
			}
			u.mu.Lock()
		}
		finish()
	case "delete":
		if r.URL.Query().Get("type") == "recycle" {
			count := len(u.recycled)
			u.recycled = map[string]writeFile{}
			respond(map[string]any{"extra": map[string]any{"succNum": count}})
			return
		}
		for _, item := range param.List {
			v, ok := u.files[key(item.Path)]
			if !ok {
				respond(map[string]any{"error_code": 31066})
				return
			}
			delete(u.files, key(item.Path))
			u.recycled[key(strconv.FormatInt(v.ID, 10))] = v
		}
		finish()
	case "restore":
		if len(param.List) != 1 {
			http.Error(w, "restore IDs", 400)
			return
		}
		id := strconv.FormatInt(param.List[0].ID, 10)
		v, ok := u.recycled[key(id)]
		if !ok {
			respond(map[string]any{"error_code": 31066})
			return
		}
		delete(u.recycled, key(id))
		u.next++
		v.ID = u.next
		u.files[key(v.Path)] = v
		respond(map[string]any{"extra": map[string]any{"list": []any{map[string]any{"fs_id": v.ID}}}})
	case "recycle/list":
		values := []any{}
		for k, v := range u.recycled {
			if strings.HasPrefix(k, owner+"\x00") {
				values = append(values, map[string]any{"fs_id": v.ID, "path": v.Path, "server_filename": path.Base(v.Path), "size": len(v.Body), "isdir": 0, "leftTime": -17, "md5": writeDigest(v.Body)})
			}
		}
		respond(map[string]any{"errno": 0, "list": values})
	case "recycle/delete":
		var ids []int64
		json.Unmarshal([]byte(r.Form.Get("fidlist")), &ids)
		for _, id := range ids {
			delete(u.recycled, key(strconv.FormatInt(id, 10)))
		}
		finish()
	case "share/pset":
		u.period, _ = strconv.Atoi(r.Form.Get("period"))
		if u.period == 99999 {
			respond(map[string]any{"errno": -7})
			return
		}
		u.next++
		id := strconv.FormatInt(u.next, 10)
		u.shares[key(id)] = map[string]any{"shareId": u.next, "fsIds": []int64{11}, "shortlink": "https://pan.baidu.com/s/1fixture", "status": 0, "public": 0, "typicalCategory": 1, "typicalPath": "/round/source", "expiredType": u.period, "expiredTime": 0, "vCnt": 7}
		if u.drop == op {
			u.drop = ""
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]any{"errno": 0, "shareid": u.next, "link": "https://pan.baidu.com/s/1fixture"})
	case "share/record":
		values := []any{}
		for k, v := range u.shares {
			if strings.HasPrefix(k, owner+"\x00") {
				values = append(values, v)
			}
		}
		respond(map[string]any{"errno": 0, "list": values})
	case "share/surlinfoinrecord":
		id := r.URL.Query().Get("shareid")
		if _, ok := u.shares[key(id)]; !ok {
			respond(map[string]any{"errno": -9})
			return
		}
		respond(map[string]any{"errno": 0, "shorturl": "1fixture", "pwd": "a1B2"})
	case "share/cancel":
		var ids []int64
		json.Unmarshal([]byte(r.Form.Get("shareid_list")), &ids)
		for _, id := range ids {
			delete(u.shares, key(strconv.FormatInt(id, 10)))
		}
		finish()
	case "share/verify":
		if r.Form.Get("pwd") != "a1B2" {
			respond(map[string]any{"errno": -9})
			return
		}
		respond(map[string]any{"errno": 0, "randsk": "PRIVATE_RANDSK_TEST_ONLY"})
	case "share/list":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		values := []any{}
		dir := r.URL.Query().Get("dir")
		total := 205
		if dir != "" {
			total = 3
		}
		for index := (page - 1) * 100; index < min(page*100, total); index++ {
			id := int64(10000 + index)
			p := fmt.Sprintf("/shared/f-%03d", index)
			if dir != "" {
				id = int64(20000 + index)
				p = fmt.Sprintf("%s/nested-%d", dir, index)
			}
			values = append(values, map[string]any{"fs_id": strconv.FormatInt(id, 10), "path": p, "server_filename": path.Base(p), "size": "7", "md5": writeDigest([]byte("payload")), "isdir": 0})
		}
		if dir == "" && page == 3 {
			values = append(values, map[string]any{"fs_id": 777, "path": "/shared/subdir", "server_filename": "subdir", "size": 0, "isdir": 1})
		}
		respond(map[string]any{"errno": 0, "list": values})
	case "share/transfer":
		var ids []int64
		json.Unmarshal([]byte(r.Form.Get("fsidlist")), &ids)
		target := r.Form.Get("path")
		info := []any{}
		for _, id := range ids {
			u.transferIDs = append(u.transferIDs, id)
			name := fmt.Sprintf("f-%03d", id-10000)
			if id >= 20000 {
				name = fmt.Sprintf("nested-%d", id-20000)
			}
			if id == 777 {
				name = "subdir"
			}
			p := path.Join(target, name)
			u.next++
			u.files[key(p)] = writeFile{ID: u.next, Path: p, Body: []byte("payload"), Dir: id == 777}
			info = append(info, map[string]any{"fsid": strconv.FormatInt(u.next, 10), "path": p, "errno": 0})
		}
		if u.drop == op {
			u.drop = ""
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]any{"errno": 0, "info": info})
	case "add_task":
		u.next++
		id := strconv.FormatInt(u.next, 10)
		u.offline[key(id)] = map[string]string{"task_id": id, "status": "1", "file_size": "7", "finished_size": "0", "create_time": strconv.FormatInt(time.Now().Unix(), 10), "start_time": "0", "finish_time": "0", "save_path": r.URL.Query().Get("save_path"), "source_url": r.URL.Query().Get("source_url"), "task_name": "fixture", "od_type": "0"}
		if u.drop == op {
			u.drop = ""
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]any{"task_id": u.next})
	case "list_task":
		if r.URL.Query().Get("start") != "0" || r.URL.Query().Get("limit") != "1000" {
			http.Error(w, "invented upstream pagination", 400)
			return
		}
		values := []any{}
		keys := []string{}
		for k := range u.offline {
			if strings.HasPrefix(k, owner+"\x00") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys[:min(len(keys), 1000)] {
			values = append(values, u.offline[k])
		}
		respond(map[string]any{"task_info": values})
	case "query_task":
		ids := strings.Split(r.URL.Query().Get("task_ids"), ",")
		values := map[string]any{}
		for _, id := range ids {
			if v, ok := u.offline[key(id)]; ok {
				values[id] = v
			}
		}
		respond(map[string]any{"task_info": values})
	case "cancel_task":
		id := r.URL.Query().Get("task_id")
		if v := u.offline[key(id)]; v != nil {
			v["status"] = "7"
		}
		finish()
	case "delete_task":
		delete(u.offline, key(r.URL.Query().Get("task_id")))
		finish()
	case "clear_task":
		count := len(u.offline)
		u.offline = map[string]map[string]string{}
		respond(map[string]any{"total": count})
	case "download":
		v, ok := u.files[key(r.URL.Query().Get("path"))]
		if !ok {
			http.NotFound(w, r)
			return
		}
		start, end := int64(0), int64(len(v.Body)-1)
		if raw := r.Header.Get("Range"); raw != "" {
			fmt.Sscanf(raw, "bytes=%d-%d", &start, &end)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(v.Body)))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(206)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(v.Body)))
		}
		w.Write(v.Body[start : end+1])
	default:
		if strings.HasPrefix(r.URL.Path, "/s/") {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `locals.mset({"loginstate":1,"shareid":9007199254740993,"share_uk":123,"uk":123,"bdstoken":"PRIVATE_TOKEN_TEST_ONLY"});`)
			return
		}
		if op == "filemetas" {
			var ids []int64
			json.Unmarshal([]byte(r.URL.Query().Get("fsids")), &ids)
			info := []any{}
			for _, v := range u.files {
				for _, id := range ids {
					if v.ID == id {
						info = append(info, map[string]any{"fs_id": v.ID, "path": v.Path})
					}
				}
			}
			respond(map[string]any{"errno": 0, "info": info})
			return
		}
		http.Error(w, "unexpected controlled operation", 400)
	}
}
