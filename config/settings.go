package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/xdg"
	"gopkg.in/ini.v1"
)

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

var configFilePath string
var cfg *ini.File

type IniFile struct {
	*General
	*Keymap
	*Ui
	*Colors
}

type General struct {
	Theme               string
	DownloadPath        string
	PreviewPath         string
	CmdPrefix           string
	ShowCommand         string
	EnableNotifications bool
	UseTerminalBell     bool
	NotificationTimeout int64
	BacklogMsgQuantity  int
	HistoryWindowMin    int64
	ChatListMode        string
	EnableDiagnostics   bool
	DebugInputEvents    bool
	DebugEventFlow      bool
	DebugUiUpdates      bool
	DiagnosticsLogPath  string
	Profile             string
	EnablePassphrase    bool
	PassphraseHash      string
}

type Keymap struct {
	SwitchPanels    string
	FocusMessages   string
	FocusInput      string
	FocusChats      string
	OpenChat        string
	Copyuser        string
	Pasteuser       string
	CommandBacklog  string
	CommandRead     string
	CommandConnect  string
	CommandQuit     string
	CommandHelp     string
	MessageDownload string
	MessageOpen     string
	MessageShow     string
	MessageUrl      string
	MessageInfo     string
	MessageRevoke   string
}

type Ui struct{ ChatSidebarWidth int }

type Colors struct {
	Background       string
	Text             string
	ForwardedText    string
	ListHeader       string
	ListContact      string
	ListGroup        string
	ListSelected     string
	ChatContact      string
	ChatMe           string
	Borders          string
	InputBackground  string
	InputText        string
	UnreadCount      string
	Positive         string
	Negative         string
	Timestamp        string
	SearchBackground string
}

var Config = IniFile{
	&General{
		Theme:               "warm",
		DownloadPath:        GetHomeDir() + "Downloads",
		PreviewPath:         GetHomeDir() + "Downloads",
		CmdPrefix:           "/",
		ShowCommand:         "jp2a --color",
		EnableNotifications: false,
		UseTerminalBell:     false,
		NotificationTimeout: 60,
		BacklogMsgQuantity:  10,
		HistoryWindowMin:    0,
		ChatListMode:        "recency_only",
		EnableDiagnostics:   false,
		DebugInputEvents:    false,
		DebugEventFlow:      false,
		DebugUiUpdates:      false,
		DiagnosticsLogPath:  "",
	},
	&Keymap{
		SwitchPanels:    "Tab",
		FocusMessages:   "Ctrl+w",
		FocusInput:      "Ctrl+Space",
		FocusChats:      "Ctrl+e",
		OpenChat:        "Enter",
		CommandBacklog:  "Ctrl+b",
		CommandRead:     "Ctrl+n",
		Copyuser:        "Ctrl+c",
		Pasteuser:       "Ctrl+v",
		CommandConnect:  "Ctrl+r",
		CommandQuit:     "Ctrl+q",
		CommandHelp:     "F1",
		MessageDownload: "d",
		MessageInfo:     "i",
		MessageOpen:     "o",
		MessageUrl:      "u",
		MessageRevoke:   "r",
		MessageShow:     "s",
	},
	&Ui{ChatSidebarWidth: 30},
	&Colors{},
}

func InitConfig() {
	var err error
	ApplyTheme(RetroTheme())
	if configFilePath, err = xdg.ConfigFile("whatscli/whatscli.config"); err == nil {
		if cfg, err = ini.Load(configFilePath); err == nil {
			fmt.Println("Loaded config:", configFilePath)
			cfg.NameMapper = ini.TitleUnderscore
			cfg.ValueMapper = os.ExpandEnv
			if section, err := cfg.GetSection("general"); err == nil {
				section.MapTo(&Config.General)
			}
			if section, err := cfg.GetSection("keymap"); err == nil {
				section.MapTo(&Config.Keymap)
			}
			if section, err := cfg.GetSection("ui"); err == nil {
				section.MapTo(&Config.Ui)
			}
			// ponytail: single theme for now; add a registry when a second theme lands
			if Config.General.Theme == "warm" {
				ApplyTheme(RetroTheme())
			}
			if section, err := cfg.GetSection("colors"); err == nil {
				section.MapTo(&Config.Colors)
			}
		} else {
			cfg = ini.Empty()
			cfg.NameMapper = ini.TitleUnderscore
			cfg.ValueMapper = os.ExpandEnv
			if err = ini.ReflectFromWithMapper(cfg, &Config, ini.TitleUnderscore); err == nil {
				err = cfg.SaveTo(configFilePath)
			}
		}
	}
	if err != nil {
		fmt.Print(err.Error())
	}
}

func GetConfigFilePath() string { return configFilePath }

// SaveNotifications toggles notifications and persists the setting to the config file.
func SaveNotifications(enabled bool) {
	Config.General.EnableNotifications = enabled
	if cfg == nil {
		return
	}
	if section, err := cfg.GetSection("general"); err == nil {
		section.Key("enable_notifications").SetValue(fmt.Sprintf("%v", enabled))
		if err = cfg.SaveTo(configFilePath); err != nil {
			fmt.Print(err.Error())
		}
	}
}

func GetSessionFilePath() string {
	name := "session"
	// "default" is an alias for the unnamed profile: both map to session.db,
	// so "/profile default" returns to the original account instead of
	// creating an empty session.default.db.
	if p := Config.General.Profile; p != "" && p != "default" {
		name = "session." + p
	}
	if sessionFilePath, err := xdg.ConfigFile("whatscli/" + name); err == nil {
		return sessionFilePath
	}
	return GetHomeDir() + "." + name
}

// AvailableProfiles lists the profile names that have a stored session DB,
// derived from the session*.db files in the config directory. The unnamed
// profile is reported as "default".
func AvailableProfiles() []string {
	dir := filepath.Dir(GetSessionFilePath())
	files, err := filepath.Glob(filepath.Join(dir, "session*.db"))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".db")
		if name == "session" {
			name = "default"
		} else {
			name = strings.TrimPrefix(name, "session.")
		}
		names = append(names, name)
	}
	return names
}

// ValidProfileName reports whether name is safe to embed in a session file path.
func ValidProfileName(name string) bool {
	return name != "" && profileNameRe.MatchString(name)
}

// SessionDbFileExists reports whether a stored session exists for the active profile.
func SessionDbFileExists() bool {
	_, err := os.Stat(GetSessionFilePath() + ".db")
	return err == nil
}

// SaveGeneralKeys persists the given [general] section keys to the config file.
func SaveGeneralKeys(keys map[string]string) {
	if cfg == nil {
		return
	}
	section, err := cfg.GetSection("general")
	if err != nil {
		return
	}
	for k, v := range keys {
		section.Key(k).SetValue(v)
	}
	if err := cfg.SaveTo(configFilePath); err != nil {
		fmt.Print(err.Error())
	}
}

func GetHomeDir() string {
	usr, _ := user.Current()
	return usr.HomeDir + string(os.PathSeparator)
}
