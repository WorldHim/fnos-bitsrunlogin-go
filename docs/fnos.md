# 飞牛 fnOS

本项目提供 fnOS 的 `.fpk` 安装包，安装后可在飞牛桌面直接打开图形界面管理登录：
填账号密码、看运行状态、切换守护模式、看日志，都不需要登录 SSH 改配置文件。

- 支持架构：x86（amd64）与 arm（arm64）
- 安装包位置：[GitHub Releases](https://github.com/WorldHim/fnos-bitsrunlogin-go/releases) 中的 `bitsrunlogin-go_<版本>_x86.fpk` / `_arm.fpk`
- 安装方式：应用中心 → 手动安装（安装包未上架官方商店，见下方说明）

## 安装

1. 在 [Releases](https://github.com/WorldHim/fnos-bitsrunlogin-go/releases) 下载与设备架构一致的 `.fpk`
   - 不确定架构时，飞牛的「系统设置 → 关于」里可以看到设备型号与架构
2. 打开飞牛「应用中心」→ 右上角「手动安装」，选择下载好的 `.fpk`
3. 安装向导会要求填写**监听端口**，默认 `9877`。这个端口只在设备本机回环地址上监听，
   由飞牛桌面反向代理访问，不需要在防火墙开放，通常也不需要修改
4. 安装完成后在桌面上会出现「深澜校园网登录」图标

首次打开时账号是空的，需要在「设置」页填写认证地址、账号与密码，然后点「保存」。
守护模式默认开启，保存后即开始按周期尝试登录。

## 关于未签名

官方 fnpack 在签名模式下只接受飞牛官方签发的开发者证书，本项目没有申请该证书，
因此安装包是以 `--nosign` 构建的。这带来两个后果：

- **可以**通过应用中心的「手动安装」正常安装使用
- **不能**上传到飞牛官方应用商店

如果哪天拿到证书，把 `scripts/fnos/build.sh` 里的 `--nosign` 去掉、让 fnpack 提供
`--key` / `--cert` 即可，其余流程不变。

## 界面

界面风格参考 LuCI：顶栏五个页签，内容区为分节的表单，右侧有操作按钮。

| 页签 | 内容 |
| --- | --- |
| 概览 | 运行状态、上次/下次执行时间、失败次数、当前账号摘要、服务启停按钮、立即登录 |
| 设置 | 认证地址、账号密码、运营商、网络（HTTPS、超时、多网卡）、守护周期、日志级别 |
| DDNS | 开关、域名、TTL、服务商（Cloudflare / 阿里云 / DNSPod）及凭据 |
| 高级 | acid / enc / os 等认证参数、User-Agent、Reality 探测地址、导出脱敏配置 |
| 日志 | 实时日志（2 秒轮询），按级别着色，可暂停与清空 |

密码与各种 Token 不会被回显：输入框初始为空，占位符提示「已设置，留空则不修改」。
留空保存等于不改动，要清空请用字段旁边的清除操作。

## 配置与数据放在哪

安装后配置不在程序目录里，否则升级换掉程序目录时会被一起删掉：

| 路径 | 内容 |
| --- | --- |
| `/vol{n}/@appdata/bitsrunlogin-go/Config.yaml` | 配置文件（权限 600） |
| `/vol{n}/@appdata/bitsrunlogin-go/info.log` | 服务输出与生命周期日志 |
| `/vol{n}/@appdata/bitsrunlogin-go/logs/` | 应用自身按配置写的日志文件 |
| `/vol{n}/@appdata/bitsrunlogin-go/port` | 当前监听端口 |

配置文件格式与命令行版本完全一致，可以直接从命令行版本的 `Config.yaml` 复制过来，
或者反过来把这里的内容拿给命令行版本用。

## 与命令行版本的关系

同一个二进制，同一个配置文件格式。区别只在于启动参数：

```shell
# 命令行：执行一次登录后退出
bitsrun --config /path/to/Config.yaml

# 服务模式：常驻，带 Web 界面，由生命周期脚本使用
bitsrun --server --server-addr 127.0.0.1:9877 --config /path/to/Config.yaml
```

`--server` 模式下，若配置中 `settings.guardian.enable` 为真则按 `duration` 周期循环登录，
为假则只尝试登录一次——与命令行版本的行为一致。

## 自己打包

需要 Go、Python（用于同步界面资源）和官方 [fnpack](https://developer.fnnas.com/docs/cli/fnpack/)。

```shell
# 打 x86 包，产物在 build/fnos/
./scripts/fnos/build.sh --arch x86

# 打 arm 包
./scripts/fnos/build.sh --arch arm

# 只准备打包目录、不调用 fnpack（排查结构问题时用）
./scripts/fnos/build.sh --arch x86 --stage-only
```

脚本做三件事：同步 Web 界面到嵌入目录、交叉编译 Go 二进制、调用 fnpack 打包。

> **Windows 上 fnpack 会失败**，报 `sync <路径>: Access is denied`。这不是包结构的问题
> （fnpack 已经通过 `Verifying files...` 那一步），而是 fnpack 1.3.x 在 Windows 上写临时
> 归档时的缺陷。请改用 Linux、WSL，或者直接使用 GitHub Actions 构建出的产物
> （`.github/workflows/release-fnos.yml`，推 tag 自动出包）。

生命周期脚本的行为有独立测试，不依赖 fnOS 机器：

```shell
./scripts/fnos/test-lifecycle.sh
```

它用一个假的二进制替换真实服务，检查目录布局、配置文件生成与幂等、端口解析、
启动/停止/状态退出码、失效与回收的 pid 文件，以及修改设置后的重启流程。

## 排查

**桌面图标点开是空白页**

先看服务有没有起来：

```shell
sudo -u bitsrunlogin-go /var/apps/bitsrunlogin-go/target/cmd/main status
```

退出码 0 表示在跑，3 表示没跑。没跑时看日志：

```shell
tail -n 50 /vol{n}/@appdata/bitsrunlogin-go/info.log
```

**启动失败，日志里有 `listen tcp ... bind: address already in use`**

端口被别的程序占了。在「应用中心 → 深澜校园网登录 → 设置」里换一个端口，
或先确认 `9877` 被谁占用。

**登录一直失败**

打开「设置」把日志级别切到调试，回「日志」页看具体报错。常见原因：
认证地址不对（应为校园网认证页域名，不要带 `http://`）、运营商选错、学校要求
HTTPS 或特殊 `acid`（在「高级」页）。

**卸载后想彻底清干净**

应用中心卸载会移除程序目录，但配置与日志在 `@appdata` 下，删掉对应的
`/vol{n}/@appdata/bitsrunlogin-go/` 目录即可。

## 权限说明

安装包以 `run-as: package` 运行，使用独立系统用户，**不使用 root**。登录过程只是
普通的出站 HTTP 请求，改 IP、改路由之类的操作都不涉及，因此不需要特权。
配置里可能出现校园网密码与 DDNS 凭据，文件权限为 600，只有该用户与 root 可读。
