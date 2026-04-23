package provider

// GoogleScopeAliases returns a copy of the Google alias map for display.
func GoogleScopeAliases() map[string]string {
	out := make(map[string]string, len(googleAliases))
	for k, v := range googleAliases {
		out[k] = v
	}
	return out
}

// MicrosoftScopeAliases returns a copy of the Microsoft alias map for display.
func MicrosoftScopeAliases() map[string]string {
	out := make(map[string]string, len(microsoftAliases))
	for k, v := range microsoftAliases {
		out[k] = v
	}
	return out
}
