package notion

import "context"

// OAuthToken contains the connection details returned by a successful exchange.
type OAuthToken struct {
	AccessToken          string  `json:"access_token"`
	WorkspaceName        string  `json:"workspace_name"`
	WorkspaceIcon        string  `json:"workspace_icon"`
	BotID                string  `json:"bot_id"`
	DuplicatedTemplateID *string `json:"duplicated_template_id"`
}

// ExchangeCode exchanges an authorization code with client HTTP Basic authentication.
func (c *Client) ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (OAuthToken, error) {
	var t OAuthToken
	err := c.json(ctx, auth{clientID: clientID, secret: clientSecret, code: code}, "POST", "/oauth/token", map[string]string{"grant_type": "authorization_code", "code": code, "redirect_uri": redirectURI}, &t)
	return t, err
}
