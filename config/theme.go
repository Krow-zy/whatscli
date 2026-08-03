package config

type Theme struct {
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

func RetroTheme() Theme {
	return Theme{
		Background:       "#1B1E24",
		Text:             "#E7DFC4",
		ForwardedText:    "#C6A86B",
		ListHeader:       "#F1EBD9",
		ListContact:      "#AEA23A",
		ListGroup:        "#8FAF87",
		ListSelected:     "#5F6B1E",
		ChatContact:      "#C7C24A",
		ChatMe:           "#5B9CA0",
		Borders:          "#B7A16A",
		InputBackground:  "#7FA69E",
		InputText:        "#1B1E24",
		UnreadCount:      "#C6A86B",
		Positive:         "#8FAF87",
		Negative:         "#C97A6D",
		Timestamp:        "#7B818C",
		SearchBackground: "#2C313A",
	}
}

func ApplyTheme(t Theme) {
	Config.Colors.Background = t.Background
	Config.Colors.Text = t.Text
	Config.Colors.ForwardedText = t.ForwardedText
	Config.Colors.ListHeader = t.ListHeader
	Config.Colors.ListContact = t.ListContact
	Config.Colors.ListGroup = t.ListGroup
	Config.Colors.ListSelected = t.ListSelected
	Config.Colors.ChatContact = t.ChatContact
	Config.Colors.ChatMe = t.ChatMe
	Config.Colors.Borders = t.Borders
	Config.Colors.InputBackground = t.InputBackground
	Config.Colors.InputText = t.InputText
	Config.Colors.UnreadCount = t.UnreadCount
	Config.Colors.Positive = t.Positive
	Config.Colors.Negative = t.Negative
	Config.Colors.Timestamp = t.Timestamp
	Config.Colors.SearchBackground = t.SearchBackground
}
