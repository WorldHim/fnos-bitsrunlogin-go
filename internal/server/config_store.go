package server

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/pkg/srun"
)

// ErrInvalidConfig is wrapped by validation failures so handlers can answer
// with 400 instead of 500.
var ErrInvalidConfig = errors.New("invalid config")

// ConfigStore reads and writes the configuration file behind the API.
//
// Writes are serialised and atomic: the new document is marshalled in memory,
// validated, written to a temporary file next to the target, and renamed over
// it. A crash therefore leaves either the old or the new file, never a
// truncated one, which matters because the login loop reloads this file after
// every save.
type ConfigStore struct {
	path string
	mu   sync.Mutex
}

// NewConfigStore returns a store for the given configuration path.
func NewConfigStore(path string) *ConfigStore {
	return &ConfigStore{path: path}
}

// Path returns the file the store is bound to.
func (c *ConfigStore) Path() string {
	return c.path
}

// Load reads and parses the file without touching global state.
func (c *ConfigStore) Load() (*config.ConfFromFile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadLocked()
}

func (c *ConfigStore) loadLocked() (*config.ConfFromFile, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	var fileConf config.ConfFromFile
	if err := unmarshalForPath(c.path, data, &fileConf); err != nil {
		return nil, err
	}
	return &fileConf, nil
}

// Save validates conf, writes it atomically and refreshes the package level
// configuration so the running supervisor picks the change up.
func (c *ConfigStore) Save(conf *config.ConfFromFile) error {
	if conf == nil {
		return fmt.Errorf("%w: empty config", ErrInvalidConfig)
	}
	if err := validate(conf); err != nil {
		return err
	}

	data, err := marshalForPath(c.path, conf)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if dir := filepath.Dir(c.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
	}

	tmp := c.path + ".tmp"
	// 0600: the file holds the account password and DDNS credentials.
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}

	// Reload so that the backoff settings and the log level match what is on
	// disk. Load also re-runs initLogFinal, which is what makes a debug_level
	// change take effect without a restart.
	return config.Load(c.path, false)
}

// MissingFieldError reports the first required setting that is empty. It is
// what tells a fresh install apart from a configured one: the package ships
// defaults, and logging in with an empty account only produces a confusing
// parse error from the campus portal.
type MissingFieldError struct {
	Field string
}

func (e *MissingFieldError) Error() string {
	return e.Field + " is required"
}

// Is lets callers treat a missing field as ErrInvalidConfig.
func (e *MissingFieldError) Is(target error) bool {
	return target == ErrInvalidConfig
}

// CheckConfigured reports the first required login setting that is missing.
//
// The default configuration written on first install leaves the account empty,
// so the service must not start the login loop until this returns nil.
func CheckConfigured(conf *config.ConfFromFile) error {
	if conf == nil {
		return &MissingFieldError{Field: "form"}
	}
	if strings.TrimSpace(conf.Form.Domain) == "" {
		return &MissingFieldError{Field: "form.domain"}
	}
	if strings.TrimSpace(conf.Form.Username) == "" {
		return &MissingFieldError{Field: "form.username"}
	}
	if conf.Form.Password == "" {
		return &MissingFieldError{Field: "form.password"}
	}
	return nil
}

// validate nudges the defaults and rejects configurations that would make the
// login loop fail later in a confusing way, such as an empty password.
func validate(conf *config.ConfFromFile) error {
	if err := CheckConfigured(conf); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidConfig, err)
	}

	settings := conf.Settings
	if settings.Basic.Timeout == 0 {
		settings.Basic.Timeout = 5
	}
	if settings.Guardian.Duration == 0 {
		settings.Guardian.Duration = 300
	}

	if settings.DDNS.Enable {
		switch settings.DDNS.Provider {
		case "aliyun":
			if settings.DDNS.Config.Aliyun.AccessKeyId == "" || settings.DDNS.Config.Aliyun.AccessKeySecret == "" {
				return fmt.Errorf("%w: aliyun access_key_id and access_key_secret are required", ErrInvalidConfig)
			}
		case "cloudflare":
			if settings.DDNS.Config.Cloudflare.Zone == "" || settings.DDNS.Config.Cloudflare.Token == "" {
				return fmt.Errorf("%w: cloudflare zone and token are required", ErrInvalidConfig)
			}
		case "dnspod":
			if settings.DDNS.Config.DnsPod.SecretId == "" || settings.DDNS.Config.DnsPod.SecretKey == "" {
				return fmt.Errorf("%w: dnspod secret_id and secret_key are required", ErrInvalidConfig)
			}
		default:
			return fmt.Errorf("%w: unsupported ddns provider %q", ErrInvalidConfig, settings.DDNS.Provider)
		}
		if strings.TrimSpace(settings.DDNS.Domain) == "" {
			return fmt.Errorf("%w: ddns.domain is required", ErrInvalidConfig)
		}
	}

	conf.Settings = settings
	return nil
}

// ConfigView is the redacted, UI friendly form of the configuration.
//
// The API never returns the raw configuration: that would hand the account
// password and the DDNS credentials to anything able to reach the port.
type ConfigView struct {
	Form     FormView       `json:"form"`
	Meta     srun.LoginMeta `json:"meta"`
	Settings SettingsView   `json:"settings"`
}

// FormView mirrors srun.LoginForm but replaces the password with a flag.
type FormView struct {
	Domain      string `json:"domain"`
	Username    string `json:"username"`
	UserType    string `json:"user_type"`
	HasPassword bool   `json:"has_password"`
}

// SettingsView mirrors config.SettingsConf with the secrets stripped.
type SettingsView struct {
	Basic          config.BasicConf                  `json:"basic"`
	Guardian       config.GuardianConf               `json:"guardian"`
	Backoff        config.BackoffConf                `json:"backoff"`
	Log            config.LogConf                    `json:"log"`
	DDNS           DdnsView                          `json:"ddns"`
	Reality        config.RealityConf                `json:"reality"`
	CustomHeader   map[string]any                    `json:"custom_header,omitempty"`
	InterfaceForms map[string]srun.LoginFormOverride `json:"interface_forms,omitempty"`
}

// DdnsView carries the non secret DDNS settings plus per provider flags.
type DdnsView struct {
	Enable   bool             `json:"enable"`
	TTL      uint             `json:"ttl"`
	Domain   string           `json:"domain"`
	Provider string           `json:"provider"`
	Config   DdnsProviderView `json:"config"`
}

// DdnsProviderView reports which credentials are already stored, so the UI can
// show "stored" instead of inventing a masked value.
type DdnsProviderView struct {
	AliyunAccessKeyId     string `json:"aliyun_access_key_id,omitempty"`
	HasAliyunAccessKey    bool   `json:"has_aliyun_access_key"`
	HasAliyunAccessSecret bool   `json:"has_aliyun_access_secret"`
	CloudflareZone        string `json:"cloudflare_zone,omitempty"`
	HasCloudflareToken    bool   `json:"has_cloudflare_token"`
	DnspodSecretId        string `json:"dnspod_secret_id,omitempty"`
	HasDnspodSecretKey    bool   `json:"has_dnspod_secret_key"`
}

// NewConfigView renders conf without any secret.
func NewConfigView(conf *config.ConfFromFile) ConfigView {
	ddns := conf.Settings.DDNS
	return ConfigView{
		Form: FormView{
			Domain:      conf.Form.Domain,
			Username:    conf.Form.Username,
			UserType:    conf.Form.UserType,
			HasPassword: conf.Form.Password != "",
		},
		Meta: conf.Meta,
		Settings: SettingsView{
			Basic:          conf.Settings.Basic,
			Guardian:       conf.Settings.Guardian,
			Backoff:        conf.Settings.Backoff,
			Log:            conf.Settings.Log,
			Reality:        conf.Settings.Reality,
			CustomHeader:   conf.Settings.CustomHeader,
			InterfaceForms: conf.Settings.InterfaceForms,
			DDNS: DdnsView{
				Enable:   ddns.Enable,
				TTL:      ddns.TTL,
				Domain:   ddns.Domain,
				Provider: ddns.Provider,
				Config: DdnsProviderView{
					AliyunAccessKeyId:     ddns.Config.Aliyun.AccessKeyId,
					HasAliyunAccessKey:    ddns.Config.Aliyun.AccessKeyId != "",
					HasAliyunAccessSecret: ddns.Config.Aliyun.AccessKeySecret != "",
					CloudflareZone:        ddns.Config.Cloudflare.Zone,
					HasCloudflareToken:    ddns.Config.Cloudflare.Token != "",
					DnspodSecretId:        ddns.Config.DnsPod.SecretId,
					HasDnspodSecretKey:    ddns.Config.DnsPod.SecretKey != "",
				},
			},
		},
	}
}

// ConfigPatch is the body accepted by PUT /api/config.
//
// Secrets travel as plain strings inside JSON over loopback. An empty string
// means "keep the stored value": the UI does not hold the current secret and
// clearing it by accident would silently disable DDNS. To clear a secret the
// client sends the matching clear_* flag instead.
type ConfigPatch struct {
	Form     *FormPatch      `json:"form"`
	Meta     *srun.LoginMeta `json:"meta"`
	Settings *SettingsPatch  `json:"settings"`
}

// FormPatch is the partial update of the login form.
type FormPatch struct {
	Domain        *string `json:"domain"`
	Username      *string `json:"username"`
	UserType      *string `json:"user_type"`
	Password      string  `json:"password"`
	ClearPassword bool    `json:"clear_password"`
}

// SettingsPatch is the partial update of the settings block.
type SettingsPatch struct {
	Basic          *config.BasicConf                 `json:"basic"`
	Guardian       *config.GuardianConf              `json:"guardian"`
	Backoff        *config.BackoffConf               `json:"backoff"`
	Log            *config.LogConf                   `json:"log"`
	DDNS           *DdnsPatch                        `json:"ddns"`
	Reality        *config.RealityConf               `json:"reality"`
	CustomHeader   map[string]any                    `json:"custom_header"`
	InterfaceForms map[string]srun.LoginFormOverride `json:"interface_forms"`
}

// DdnsPatch is the partial update of the DDNS block.
type DdnsPatch struct {
	Enable   *bool              `json:"enable"`
	TTL      *uint              `json:"ttl"`
	Domain   *string            `json:"domain"`
	Provider *string            `json:"provider"`
	Config   *DdnsProviderPatch `json:"config"`
}

// DdnsProviderPatch is the partial update of the provider credentials.
type DdnsProviderPatch struct {
	AliyunAccessKeyId     string `json:"aliyun_access_key_id"`
	AliyunAccessKeySecret string `json:"aliyun_access_key_secret"`
	ClearAliyunAccessKey  bool   `json:"clear_aliyun_access_key"`
	CloudflareZone        string `json:"cloudflare_zone"`
	CloudflareToken       string `json:"cloudflare_token"`
	ClearCloudflareToken  bool   `json:"clear_cloudflare_token"`
	DnspodSecretId        string `json:"dnspod_secret_id"`
	DnspodSecretKey       string `json:"dnspod_secret_key"`
	ClearDnspodSecretKey  bool   `json:"clear_dnspod_secret_key"`
}

// Apply merges the patch into conf in place, preserving secrets the patch left
// empty.
func (p *ConfigPatch) Apply(conf *config.ConfFromFile) {
	if p.Form != nil {
		f := p.Form
		if f.Domain != nil {
			conf.Form.Domain = *f.Domain
		}
		if f.Username != nil {
			conf.Form.Username = *f.Username
		}
		if f.UserType != nil {
			conf.Form.UserType = *f.UserType
		}
		switch {
		case f.ClearPassword:
			conf.Form.Password = ""
		case f.Password != "":
			conf.Form.Password = f.Password
		}
	}

	if p.Meta != nil {
		conf.Meta = *p.Meta
		if conf.Meta.OS == "" {
			// os and name describe the same thing; a name without an os
			// tells the portal nothing and is just stale state from an
			// earlier save.
			conf.Meta.Name = ""
		}
	}

	if p.Settings == nil {
		return
	}
	s := p.Settings
	if s.Basic != nil {
		conf.Settings.Basic = *s.Basic
	}
	if s.Guardian != nil {
		conf.Settings.Guardian = *s.Guardian
	}
	if s.Backoff != nil {
		conf.Settings.Backoff = *s.Backoff
	}
	if s.Log != nil {
		conf.Settings.Log = *s.Log
	}
	if s.Reality != nil {
		conf.Settings.Reality = *s.Reality
	}
	if s.CustomHeader != nil {
		conf.Settings.CustomHeader = s.CustomHeader
	}
	if s.InterfaceForms != nil {
		conf.Settings.InterfaceForms = s.InterfaceForms
	}

	if s.DDNS == nil {
		return
	}
	d := s.DDNS
	if d.Enable != nil {
		conf.Settings.DDNS.Enable = *d.Enable
	}
	if d.TTL != nil {
		conf.Settings.DDNS.TTL = *d.TTL
	}
	if d.Domain != nil {
		conf.Settings.DDNS.Domain = *d.Domain
	}
	if d.Provider != nil {
		conf.Settings.DDNS.Provider = *d.Provider
	}
	if d.Config == nil {
		return
	}
	c := d.Config
	preserve(&conf.Settings.DDNS.Config.Aliyun.AccessKeyId, c.AliyunAccessKeyId, c.ClearAliyunAccessKey)
	preserve(&conf.Settings.DDNS.Config.Aliyun.AccessKeySecret, c.AliyunAccessKeySecret, c.ClearAliyunAccessKey)
	preserve(&conf.Settings.DDNS.Config.Cloudflare.Zone, c.CloudflareZone, false)
	preserve(&conf.Settings.DDNS.Config.Cloudflare.Token, c.CloudflareToken, c.ClearCloudflareToken)
	preserve(&conf.Settings.DDNS.Config.DnsPod.SecretId, c.DnspodSecretId, false)
	preserve(&conf.Settings.DDNS.Config.DnsPod.SecretKey, c.DnspodSecretKey, c.ClearDnspodSecretKey)
}

// preserve writes value into dst unless the caller asked to clear it or left
// it empty, which means "unchanged".
func preserve(dst *string, value string, clear bool) {
	switch {
	case clear:
		*dst = ""
	case value != "":
		*dst = value
	}
}

// unmarshalForPath picks the decoder from the file extension, matching what
// config.newReaderFromPath does for the CLI.
func unmarshalForPath(pathname string, data []byte, v any) error {
	switch path.Ext(pathname) {
	case ".json":
		return config.Json{}.Unmarshal(data, v)
	default:
		return config.Yaml{}.Unmarshal(data, v)
	}
}

// marshalForPath is the encoding counterpart of unmarshalForPath.
func marshalForPath(pathname string, v any) ([]byte, error) {
	switch path.Ext(pathname) {
	case ".json":
		return config.Json{}.Marshal(v)
	default:
		return config.Yaml{}.Marshal(v)
	}
}
