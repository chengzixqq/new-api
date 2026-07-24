package console_setting

import "github.com/QuantumNous/new-api/setting/config"

type ConsoleSetting struct {
	ApiInfo              string `json:"api_info"`
	Announcements        string `json:"announcements"`
	FAQ                  string `json:"faq"`
	ApiInfoEnabled       bool   `json:"api_info_enabled"`
	AnnouncementsEnabled bool   `json:"announcements_enabled"`
	FAQEnabled           bool   `json:"faq_enabled"`
}

var defaultConsoleSetting = ConsoleSetting{
	ApiInfo:              "",
	Announcements:        "",
	FAQ:                  "",
	ApiInfoEnabled:       true,
	AnnouncementsEnabled: true,
	FAQEnabled:           true,
}

var consoleSetting = defaultConsoleSetting

func init() {
	config.GlobalConfig.Register("console_setting", &consoleSetting)
}

func GetConsoleSetting() *ConsoleSetting {
	return &consoleSetting
}
