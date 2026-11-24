package main

import (
	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/flags"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/keys"
	"github.com/Mmx233/BitSrunLoginGo/internal/login"
)

func main() {
	// flags 必须在 config 之前解析：config 的初始化依赖 -config 给出的路径。
	// 注意 config 包在 init 中完成加载，因此此调用必须位于 main 的第一条语句。
	flags.Parse()

	logger := config.Logger
	if config.Settings.Basic.Interfaces != "" {
		logger.Infoln("[多网卡模式]")
	}

	if config.Settings.Guardian.Enable {
		//进入守护模式
		login.Guardian(logger.WithField(keys.LogComponent, "guard"))
	} else {
		//执行单次流程
		err := login.Login(login.Conf{
			Logger:                      logger.WithField(keys.LogComponent, "login"),
			IsOnlineDetectLogDebugLevel: false,
		})
		if err != nil {
			logger.Fatalln("login failed with error:", err)
		}
	}
}
