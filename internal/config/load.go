package config

import (
	"errors"
	"os"
	"time"

	"github.com/Mmx233/BitSrunLoginGo/internal/config/flags"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/keys"
	"github.com/Mmx233/tool"
)

// ErrConfigCreated 表示配置文件不存在，已按默认值创建。
// 调用方通常应当提示用户去编辑该文件，而不是继续执行。
var ErrConfigCreated = errors.New("default config created")

func init() {
	initLogPre()

	logger := Logger.WithField(keys.LogComponent, "init")

	// 参数必须在这里解析：config 的 init 会读取 flags.Path，而 -config 的值
	// 只有 flag.Parse 之后才存在。解析放在 flags 包里做幂等保护，因此即使
	// 调用方（例如 main）稍后再调用一次也不会重复解析。
	flags.Parse()

	if err := Load(flags.Path, true); err != nil {
		if errors.Is(err, ErrConfigCreated) {
			logger.Infoln("已生成配置文件，请编辑 '" + flags.Path + "' 然后重试")
			os.Exit(0)
		}
		logger.Fatalln(err)
	}
}

// Load 从 path 读取配置并刷新包级配置变量。
//
// create 为 true 时，path 不存在会写入默认配置并返回 ErrConfigCreated。
// create 为 false 时，path 不存在将返回错误，供不希望隐式创建文件的调用方使用
// （例如守护进程：磁盘不可写时应当报错，而不是生成一份默认配置）。
//
// Load 会修改包级状态，不要在读取 Form、Meta、Settings 的同时并发调用。
func Load(path string, create bool) error {
	reader := newReaderFromPath(path)

	// 生成配置文件
	if create {
		exist, err := tool.File.Exists(path)
		if err != nil {
			return errWrap("读取配置文件失败", err)
		} else if !exist {
			data, err := reader.Marshal(&defaultConfig)
			if err != nil {
				return errWrap("生成配置文件失败", err)
			}
			if err = os.WriteFile(path, data, 0600); err != nil {
				return errWrap("写入配置文件失败", err)
			}
			return ErrConfigCreated
		}
	}

	// 读取配置文件
	data, err := os.ReadFile(path)
	if err != nil {
		return errWrap("读取配置失败", err)
	}
	var fileConf ConfFromFile
	if err = reader.Unmarshal(data, &fileConf); err != nil {
		return errWrap("解析配置失败", err)
	}
	Form = &fileConf.Form
	Meta = &fileConf.Meta
	Settings = &fileConf.Settings
	Timeout = time.Duration(Settings.Basic.Timeout) * time.Second

	// flag 配置覆写
	if flags.Debug {
		Settings.Log.DebugLevel = true
	}
	if flags.Acid != "" {
		Meta.Acid = flags.Acid
	}

	initLogFinal()
	initBackoff()
	return nil
}

type wrappedErr struct {
	msg string
	err error
}

func (e wrappedErr) Error() string {
	return e.msg + "：" + e.err.Error()
}

func (e wrappedErr) Unwrap() error {
	return e.err
}

func errWrap(msg string, err error) error {
	return wrappedErr{msg: msg, err: err}
}
