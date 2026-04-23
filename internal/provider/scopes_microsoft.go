package provider

// microsoftAliases is the set of recognized Microsoft Graph scope names.
// Microsoft scopes are passed to Graph as-is (no URI expansion), so the map
// is identity. Full Graph URIs like "https://graph.microsoft.com/Mail.Read"
// are also accepted via the "://" passthrough in ExpandScopes.
var microsoftAliases = map[string]string{
	"Mail.Read":           "Mail.Read",
	"Mail.Send":           "Mail.Send",
	"Calendars.Read":      "Calendars.Read",
	"Calendars.ReadWrite": "Calendars.ReadWrite",
	"Chat.Read":           "Chat.Read",
	"ChatMessage.Send":    "ChatMessage.Send",
	"User.Read":           "User.Read",
	"offline_access":      "offline_access",
	"openid":              "openid",
	"email":               "email",
	"profile":             "profile",
}
