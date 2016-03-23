package main

import (
	b64 "encoding/base64"
	"crypto"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"math"

	"github.com/18F/hmacauth"
	"github.com/bitly/oauth2_proxy/providers"
)

// Configuration Options that can be set by Command Line Flag, or Config File
type Options struct {
	ProxyPrefix  string `flag:"proxy-prefix" cfg:"proxy-prefix"`
	HttpAddress  string `flag:"http-address" cfg:"http_address"`
	HttpsAddress string `flag:"https-address" cfg:"https_address"`
	RedirectURL  string `flag:"redirect-url" cfg:"redirect_url"`
	ClientID     string `flag:"client-id" cfg:"client_id" env:"OAUTH2_PROXY_CLIENT_ID"`
	ClientSecret string `flag:"client-secret" cfg:"client_secret" env:"OAUTH2_PROXY_CLIENT_SECRET"`
	TLSCertFile  string `flag:"tls-cert" cfg:"tls_cert_file"`
	TLSKeyFile   string `flag:"tls-key" cfg:"tls_key_file"`

	AuthenticatedEmailsFile  string   `flag:"authenticated-emails-file" cfg:"authenticated_emails_file"`
	AzureTenant              string   `flag:"azure-tenant" cfg:"azure_tenant"`
	EmailDomains             []string `flag:"email-domain" cfg:"email_domains"`
	GitHubOrg                string   `flag:"github-org" cfg:"github_org"`
	GitHubTeam               string   `flag:"github-team" cfg:"github_team"`
	GoogleGroups             []string `flag:"google-group" cfg:"google_group"`
	GoogleAdminEmail         string   `flag:"google-admin-email" cfg:"google_admin_email"`
	GoogleServiceAccountJSON string   `flag:"google-service-account-json" cfg:"google_service_account_json"`
	HtpasswdFile             string   `flag:"htpasswd-file" cfg:"htpasswd_file"`
	DisplayHtpasswdForm      bool     `flag:"display-htpasswd-form" cfg:"display_htpasswd_form"`
	CustomTemplatesDir       string   `flag:"custom-templates-dir" cfg:"custom_templates_dir"`

	CookieName      string        `flag:"cookie-name" cfg:"cookie_name" env:"OAUTH2_PROXY_COOKIE_NAME"`
	CookieSecret    string        `flag:"cookie-secret" cfg:"cookie_secret" env:"OAUTH2_PROXY_COOKIE_SECRET"`
	CookieSecretB64 string        `flag:"cookie-secret-b64" cfg:"cookie_secret_b64" env:"OAUTH2_PROXY_COOKIE_SECRET_B64"`
	CookieDomain    string        `flag:"cookie-domain" cfg:"cookie_domain" env:"OAUTH2_PROXY_COOKIE_DOMAIN"`
	CookieExpire    time.Duration `flag:"cookie-expire" cfg:"cookie_expire" env:"OAUTH2_PROXY_COOKIE_EXPIRE"`
	CookieRefresh   time.Duration `flag:"cookie-refresh" cfg:"cookie_refresh" env:"OAUTH2_PROXY_COOKIE_REFRESH"`
	CookieSecure    bool          `flag:"cookie-secure" cfg:"cookie_secure"`
	CookieHttpOnly  bool          `flag:"cookie-httponly" cfg:"cookie_httponly"`

	Upstreams         []string `flag:"upstream" cfg:"upstreams"`
	SkipAuthRegex     []string `flag:"skip-auth-regex" cfg:"skip_auth_regex"`
	PassBasicAuth     bool     `flag:"pass-basic-auth" cfg:"pass_basic_auth"`
	BasicAuthPassword string   `flag:"basic-auth-password" cfg:"basic_auth_password"`
	PassAccessToken   bool     `flag:"pass-access-token" cfg:"pass_access_token"`
	PassHostHeader    bool     `flag:"pass-host-header" cfg:"pass_host_header"`

	// These options allow for other providers besides Google, with
	// potential overrides.
	Provider          string `flag:"provider" cfg:"provider"`
	LoginURL          string `flag:"login-url" cfg:"login_url"`
	RedeemURL         string `flag:"redeem-url" cfg:"redeem_url"`
	ProfileURL        string `flag:"profile-url" cfg:"profile_url"`
	ProtectedResource string `flag:"resource" cfg:"resource"`
	ValidateURL       string `flag:"validate-url" cfg:"validate_url"`
	Scope             string `flag:"scope" cfg:"scope"`
	ApprovalPrompt    string `flag:"approval-prompt" cfg:"approval_prompt"`

	RequestLogging bool `flag:"request-logging" cfg:"request_logging"`

	SignatureKey string `flag:"signature-key" cfg:"signature_key" env:"OAUTH2_PROXY_SIGNATURE_KEY"`

	// internal values that are set after config validation
	redirectURL   *url.URL
	proxyURLs     []*url.URL
	CompiledRegex []*regexp.Regexp
	provider      providers.Provider
	signatureData *SignatureData
}

type SignatureData struct {
	hash crypto.Hash
	key  string
}

func NewOptions() *Options {
	return &Options{
		ProxyPrefix:         "/oauth2",
		HttpAddress:         "127.0.0.1:4180",
		HttpsAddress:        ":443",
		DisplayHtpasswdForm: true,
		CookieName:          "_oauth2_proxy",
		CookieSecure:        true,
		CookieHttpOnly:      true,
		CookieExpire:        time.Duration(168) * time.Hour,
		CookieRefresh:       time.Duration(0),
		PassBasicAuth:       true,
		PassAccessToken:     false,
		PassHostHeader:      true,
		ApprovalPrompt:      "force",
		RequestLogging:      true,
	}
}

func parseURL(to_parse string, urltype string, msgs []string) (*url.URL, []string) {
	parsed, err := url.Parse(to_parse)
	if err != nil {
		return nil, append(msgs, fmt.Sprintf(
			"error parsing %s-url=%q %s", urltype, to_parse, err))
	}
	return parsed, msgs
}

func (o *Options) Validate() error {
	msgs := make([]string, 0)
	if len(o.Upstreams) < 1 {
		msgs = append(msgs, "missing setting: upstream")
	}
	if o.CookieSecret == "" && o.CookieSecretB64 == "" {
		msgs = append(msgs, "missing setting: cookie-secret or cookie-secret-b64")
	}
	if o.ClientID == "" {
		msgs = append(msgs, "missing setting: client-id")
	}
	if o.ClientSecret == "" {
		msgs = append(msgs, "missing setting: client-secret")
	}
	if o.AuthenticatedEmailsFile == "" && len(o.EmailDomains) == 0 && o.HtpasswdFile == "" {
		msgs = append(msgs, "missing setting for email validation: email-domain or authenticated-emails-file required.\n      use email-domain=* to authorize all email addresses")
	}

	o.redirectURL, msgs = parseURL(o.RedirectURL, "redirect", msgs)

	for _, u := range o.Upstreams {
		upstreamURL, err := url.Parse(u)
		if err != nil {
			msgs = append(msgs, fmt.Sprintf(
				"error parsing upstream=%q %s",
				upstreamURL, err))
		}
		if upstreamURL.Path == "" {
			upstreamURL.Path = "/"
		}
		o.proxyURLs = append(o.proxyURLs, upstreamURL)
	}

	for _, u := range o.SkipAuthRegex {
		CompiledRegex, err := regexp.Compile(u)
		if err != nil {
			msgs = append(msgs, fmt.Sprintf(
				"error compiling regex=%q %s", u, err))
		}
		o.CompiledRegex = append(o.CompiledRegex, CompiledRegex)
	}
	msgs = parseProviderInfo(o, msgs)

	if o.PassAccessToken || (o.CookieRefresh != time.Duration(0)) {
		if o.CookieSecret == "" && o.CookieSecretB64 == "" {
			msgs = append(msgs, fmt.Sprintf(
				"cookie_secret or cookie_secret_64 is required "+
					"to create an AES cipher when "+
					"pass_access_token == true or "+
					"cookie_refresh != 0, but both are missing"))
		}
	}

	if (o.PassAccessToken || (o.CookieRefresh != time.Duration(0))) && o.CookieSecret != "" {
		valid_cookie_secret_size := false
		for _, i := range []int{16, 24, 32} {
			if len(o.CookieSecret) == i {
				valid_cookie_secret_size = true
			}
		}
		if valid_cookie_secret_size == false {
			msgs = append(msgs, fmt.Sprintf(
				"cookie_secret must be 16, 24, or 32 bytes "+
					"to create an AES cipher when "+
					"pass_access_token == true or "+
					"cookie_refresh != 0, but is %d bytes",
				len(o.CookieSecret)))
		}
	}

	if (o.PassAccessToken || (o.CookieRefresh != time.Duration(0))) && o.CookieSecretB64 != "" {
		valid_cookie_secret_size := false
		padded_secret := addPadding(o.CookieSecretB64)
		for _, i := range []int{24, 32, 44} {
			if len(padded_secret) == i {
				valid_cookie_secret_size = true
			}
		}
		if valid_cookie_secret_size == false {
			msgs = append(msgs, fmt.Sprintf(
				"cookie_secret_b64 must be 24, 32, or 44 characters (with padding) "+
					"to create an AES cipher of 16, 24, or 32 bytes when "+
					"pass_access_token == true or "+
					"cookie_refresh != 0, but is %d bytes",
				len(o.CookieSecret)))
		}
	}

	if o.CookieSecretB64 != "" {
		padded_secret := addPadding(o.CookieSecretB64)
		_, err := b64.URLEncoding.DecodeString(padded_secret)
		if err != nil {
			msgs = append(msgs, fmt.Sprintf(
				"cookie_secret_b64 must be a valid (urlsafe) base64 encoded value"))
		}
	}

	if o.CookieRefresh >= o.CookieExpire {
		msgs = append(msgs, fmt.Sprintf(
			"cookie_refresh (%s) must be less than "+
				"cookie_expire (%s)",
			o.CookieRefresh.String(),
			o.CookieExpire.String()))
	}

	if len(o.GoogleGroups) > 0 || o.GoogleAdminEmail != "" || o.GoogleServiceAccountJSON != "" {
		if len(o.GoogleGroups) < 1 {
			msgs = append(msgs, "missing setting: google-group")
		}
		if o.GoogleAdminEmail == "" {
			msgs = append(msgs, "missing setting: google-admin-email")
		}
		if o.GoogleServiceAccountJSON == "" {
			msgs = append(msgs, "missing setting: google-service-account-json")
		}
	}

	msgs = parseSignatureKey(o, msgs)

	if len(msgs) != 0 {
		return fmt.Errorf("Invalid configuration:\n  %s",
			strings.Join(msgs, "\n  "))
	}
	return nil
}

func GetCookieSecretBytes(o *Options) ([]byte, error) {
	var err error
	var secret_string []byte
	switch {
	case o.CookieSecretB64 != "":
		secret_string, err = b64.URLEncoding.DecodeString(addPadding(o.CookieSecretB64))
	case o.CookieSecret != "":
		err = nil
		secret_string = []byte(o.CookieSecret)
	}
	return secret_string, err
}

func parseProviderInfo(o *Options, msgs []string) []string {
	p := &providers.ProviderData{
		Scope:          o.Scope,
		ClientID:       o.ClientID,
		ClientSecret:   o.ClientSecret,
		ApprovalPrompt: o.ApprovalPrompt,
	}
	p.LoginURL, msgs = parseURL(o.LoginURL, "login", msgs)
	p.RedeemURL, msgs = parseURL(o.RedeemURL, "redeem", msgs)
	p.ProfileURL, msgs = parseURL(o.ProfileURL, "profile", msgs)
	p.ValidateURL, msgs = parseURL(o.ValidateURL, "validate", msgs)
	p.ProtectedResource, msgs = parseURL(o.ProtectedResource, "resource", msgs)

	o.provider = providers.New(o.Provider, p)
	switch p := o.provider.(type) {
	case *providers.AzureProvider:
		p.Configure(o.AzureTenant)
	case *providers.GitHubProvider:
		p.SetOrgTeam(o.GitHubOrg, o.GitHubTeam)
	case *providers.GoogleProvider:
		if o.GoogleServiceAccountJSON != "" {
			file, err := os.Open(o.GoogleServiceAccountJSON)
			if err != nil {
				msgs = append(msgs, "invalid Google credentials file: "+o.GoogleServiceAccountJSON)
			} else {
				p.SetGroupRestriction(o.GoogleGroups, o.GoogleAdminEmail, file)
			}
		}
	}
	return msgs
}

func parseSignatureKey(o *Options, msgs []string) []string {
	if o.SignatureKey == "" {
		return msgs
	}

	components := strings.Split(o.SignatureKey, ":")
	if len(components) != 2 {
		return append(msgs, "invalid signature hash:key spec: "+
			o.SignatureKey)
	}

	algorithm, secretKey := components[0], components[1]
	if hash, err := hmacauth.DigestNameToCryptoHash(algorithm); err != nil {
		return append(msgs, "unsupported signature hash algorithm: "+
			o.SignatureKey)
	} else {
		o.signatureData = &SignatureData{hash, secretKey}
	}
	return msgs
}

func addPadding (secret string) (string) {
	var padded_secret string

	mod_4 := math.Mod(float64(len(secret)), 4.0)
	switch {
	case mod_4 == 0:
		padded_secret = secret
	case mod_4 > 0:
		padded_secret = secret + strings.Repeat("=", int(4-mod_4))
	}
	return padded_secret
}
