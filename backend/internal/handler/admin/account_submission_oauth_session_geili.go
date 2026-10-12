package admin

func (h *OAuthHandler) ExportSubmissionSessionGeili(id string) ([]byte, error) {
	return h.oauthService.ExportSubmissionSessionGeili(id)
}
func (h *OAuthHandler) RestoreSubmissionSessionGeili(id string, raw []byte) error {
	return h.oauthService.RestoreSubmissionSessionGeili(id, raw)
}
func (h *OpenAIOAuthHandler) ExportSubmissionSessionGeili(id string) ([]byte, error) {
	return h.openaiOAuthService.ExportSubmissionSessionGeili(id)
}
func (h *OpenAIOAuthHandler) RestoreSubmissionSessionGeili(id string, raw []byte) error {
	return h.openaiOAuthService.RestoreSubmissionSessionGeili(id, raw)
}
