package pcscommand

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcslogin"
	"github.com/urfave/cli"
)

type qrLoginFunc func(context.Context, pcslogin.Options) (*pcslogin.Session, error)

// QRLoginCommand adds the native, official-App QR flow to either CLI mode.
func QRLoginCommand() cli.Command {
	return qrLoginCommand(pcsconfig.Config, func(ctx context.Context, opts pcslogin.Options) (*pcslogin.Session, error) {
		client, err := pcslogin.NewClient(nil, opts)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return client.Login(ctx)
	})
}

func qrLoginCommand(config *pcsconfig.PCSConfig, login qrLoginFunc) cli.Command {
	return cli.Command{
		Name:     "qrlogin",
		Usage:    "使用百度网盘官方 App 扫码登录",
		Category: "百度帐号",
		Description: "显示官方登录二维码，在百度网盘 App 扫码并确认后自动保存账号。\n" +
			"\t支持 Ctrl+C 取消；二维码图片在命令结束后自动删除。\n" +
			"\t示例: BaiduPCS-Go qrlogin --timeout 300\n" +
			"\t      BaiduPCS-Go qrlogin --no-qr --qr-file /tmp/baidu-login.png",
		Before: func(c *cli.Context) error { return config.Reload() },
		OnUsageError: func(*cli.Context, error, bool) error {
			return cli.NewExitError("qrlogin 参数无效，请运行 qrlogin --help 查看用法", 2)
		},
		Flags: []cli.Flag{
			cli.IntFlag{Name: "timeout", Value: 300, Usage: "等待扫码和登录的总超时，单位秒（1 至 86400）"},
			cli.StringFlag{Name: "qr-file", Usage: "二维码图片文件路径（必须尚不存在，命令结束后删除）"},
			cli.BoolFlag{Name: "no-qr", Usage: "不在终端绘制二维码，使用图片文件扫码"},
		},
		Action: func(c *cli.Context) error {
			seconds := c.Int("timeout")
			if c.NArg() != 0 || seconds < 1 || seconds > 86400 {
				return cli.NewExitError("qrlogin 不接受位置参数；--timeout 必须是 1 至 86400 的秒数", 2)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			var imagePath string
			defer func() {
				if imagePath != "" {
					os.Remove(imagePath)
				}
			}()
			opts := pcslogin.Options{Timeout: time.Duration(seconds) * time.Second, AppID: config.AppID}
			opts.OnState = func(state pcslogin.State) { fmt.Fprintln(c.App.Writer, string(state)) }
			opts.OnQRCode = func(data []byte) error {
				var file *os.File
				var err error
				if c.String("qr-file") != "" {
					file, err = os.OpenFile(c.String("qr-file"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				} else {
					file, err = os.CreateTemp("", "baidupcs-qr-*.png")
				}
				if err != nil {
					return errors.New("无法创建二维码文件，请检查路径、权限及文件是否已存在")
				}
				imagePath = file.Name()
				if _, err = file.Write(data); err != nil {
					file.Close()
					return errors.New("写入二维码文件失败")
				}
				if err = file.Close(); err != nil {
					return errors.New("关闭二维码文件失败")
				}
				if _, err = fmt.Fprintf(c.App.Writer, "二维码图片: %s（本次登录结束后自动删除）\n", imagePath); err != nil {
					return errors.New("输出二维码路径失败")
				}
				if !c.Bool("no-qr") {
					if err = pcslogin.RenderQRCode(c.App.Writer, data); err != nil {
						if _, writeErr := io.WriteString(c.App.Writer, "终端二维码显示失败，请打开上述图片扫码。\n"); writeErr != nil {
							return errors.New("输出二维码失败")
						}
					}
				}
				return nil
			}
			session, err := login(ctx, opts)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return cli.NewExitError("扫码登录已取消", 130)
				}
				if errors.Is(err, context.DeadlineExceeded) {
					return cli.NewExitError("扫码登录超时，请重新运行 qrlogin", 124)
				}
				return cli.NewExitError(err, 1)
			}
			if ctx.Err() != nil {
				return cli.NewExitError("扫码登录已取消", 130)
			}
			account, err := config.SaveQRSession(session)
			if err != nil {
				return cli.NewExitError(err, 1)
			}
			_, err = fmt.Fprintln(c.App.Writer, "百度帐号扫码登录成功:", account.Name)
			return err
		},
	}
}
