package provider

// googleAliases maps user-friendly short names to full Google scope URIs.
// Aliases that expand to multiple scopes use the "compound" list (one alias -> []string).
var googleAliases = map[string]string{
	"gmail.readonly":    "https://www.googleapis.com/auth/gmail.readonly",
	"gmail.send":        "https://www.googleapis.com/auth/gmail.send",
	"gmail.modify":      "https://www.googleapis.com/auth/gmail.modify",
	"calendar":          "https://www.googleapis.com/auth/calendar",
	"calendar.readonly": "https://www.googleapis.com/auth/calendar.readonly",
	"drive":                    "https://www.googleapis.com/auth/drive",
	"drive.readonly":           "https://www.googleapis.com/auth/drive.readonly",
	"drive.file":               "https://www.googleapis.com/auth/drive.file",
	"drive.metadata.readonly":  "https://www.googleapis.com/auth/drive.metadata.readonly",
	"contacts":          "https://www.googleapis.com/auth/contacts",
	"contacts.readonly": "https://www.googleapis.com/auth/contacts.readonly",
	"profile":           "openid email profile",
}
