package flags

import (
	"flag"
	"sync"
	"testing"
)

var parsed struct {
	sync.Mutex
	done bool
}

var (
	// Path 配置文件路径
	Path string

	Interface string
	Debug     bool
	AutoAcid  bool
	Acid      string
	AutoEnc   bool

	// Server 启动 Web 控制服务而不是直接执行登录流程。
	Server bool

	// ServerAddr 控制服务的监听地址。默认只监听回环地址：服务没有独立的
	// 登录鉴权，靠 fnOS 桌面窗口反代访问，暴露到局域网等于把配置和账号
	// 交给所有人。
	ServerAddr string

	// ServerPIDFile 让服务在启动时写下自己的 pid。置空则不写。
	//
	// fnOS 用它来判断服务是否在运行。由服务自己写能拿到确定的进程号：
	// 由启动它的脚本去记，记到的往往是切换用户那一层包装进程的 pid，
	// 那个进程在拉起义子进程后立刻退出，后续的停止和状态检查都会误判。
	ServerPIDFile string
)

func init() {
	flag.StringVar(&Path, "config", "Config.yaml", "config path")

	flag.StringVar(&Interface, "interface", "", "specify the eth name")
	flag.BoolVar(&Debug, "debug", false, "enable debug mode")
	flag.BoolVar(&AutoAcid, "auto-acid", false, "auto detect acid")
	flag.StringVar(&Acid, "acid", "", "specify acid value")
	flag.BoolVar(&AutoEnc, "auto-enc", false, "auto detect enc")

	flag.BoolVar(&Server, "server", false, "run the web control server instead of a single login")
	flag.StringVar(&ServerAddr, "server-addr", "127.0.0.1:9877", "listen address for -server")
	flag.StringVar(&ServerPIDFile, "pid-file", "", "write the process id to this file on -server startup")
}

func parseOnce() {
	parsed.Lock()
	if !parsed.done {
		// 测试进程收到的是 go test 自己的 -test.* 参数，解析它们会让本包
		// 直接退出。测试用 SetForTest 或直接给 Path 赋值，不需要命令行。
		//
		// 判断放在执行解析之前、只做一次：之后无论是否跳过都置位，避免
		// 重复进入。
		if !testing.Testing() {
			flag.Parse()
		}
		parsed.done = true
	}
	parsed.Unlock()
}

// Parse 解析命令行参数，重复调用无副作用。
//
// 解析动作没有放在 init 里：init 阶段的 flag.Parse 会对任何不认识的参数
// 直接退出，包括 go test 注入的 -test.* 标志，使本包及其下游无法被测试。
// 因此改为由第一次真正需要参数值的调用方触发——即 config 包加载配置时
// （见 config.Load）。解析必须发生在 config 的 init 读取 Path 之前，否则
// -config 指定的路径会被忽略。
func Parse() {
	parseOnce()
}

// SetForTest 覆盖参数值，仅在测试中使用。
// 置位后 Parse 不再执行，避免测试进程的真实命令行覆盖这些值。
func SetForTest(path, serverAddr string, server bool) {
	Path = path
	ServerAddr = serverAddr
	Server = server

	parsed.Lock()
	parsed.done = true
	parsed.Unlock()
}
