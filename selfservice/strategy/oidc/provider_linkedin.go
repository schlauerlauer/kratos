// Copyright © 2023 Ory Corp
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/ory/kratos/x"
	"github.com/ory/x/otelx"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/linkedin"

	"github.com/ory/herodot"
	"github.com/ory/x/httpx"
)

type LinkedInProfile struct {
	LocalizedLastName  string `json:"family_name"`
	LocalizedFirstName string `json:"given_name"`
	ProfilePicture     string `json:"picture"`
	EmailAddress       string `json:"email"`
	EmailVerified      bool   `json:"email_verified"`
	ID                 string `json:"sub"`
	Locale             *struct {
		Lanuguage string `json:"language"`
	} `json:"locale,omitempty"`
}

type LinkedInIntrospection struct {
	Active       bool   `json:"active"`
	ClientID     string `json:"client_id"`
	AuthorizedAt uint32 `json:"authorized_at"`
	CreatedAt    uint32 `json:"created_at"`
	ExpiresAt    uint32 `json:"expires_at"`
	Status       string `json:"status"`
	Scope        string `json:"scope"`
	AuthType     string `json:"auth_type"`
}

// type APIUrl string

const (
	ProfileUrl       string = "https://api.linkedin.com/v2/userinfo"
	IntrospectionURL string = "https://www.linkedin.com/oauth/v2/introspectToken"
)

type ProviderLinkedIn struct {
	config *Configuration
	reg    Dependencies
}

func NewProviderLinkedIn(
	config *Configuration,
	reg Dependencies,
) Provider {
	return &ProviderLinkedIn{
		config: config,
		reg:    reg,
	}
}

func (l *ProviderLinkedIn) Config() *Configuration {
	return l.config
}

func (l *ProviderLinkedIn) oauth2(ctx context.Context) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     l.config.ClientID,
		ClientSecret: l.config.ClientSecret,
		Endpoint:     linkedin.Endpoint,
		Scopes:       l.config.Scope,
		RedirectURL:  l.config.Redir(l.reg.Config().OIDCRedirectURIBase(ctx)),
	}
}

func (l *ProviderLinkedIn) OAuth2(ctx context.Context) (*oauth2.Config, error) {
	return l.oauth2(ctx), nil
}

func (l *ProviderLinkedIn) AuthCodeURLOptions(r ider) []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

func (l *ProviderLinkedIn) fetch(ctx context.Context, client *retryablehttp.Client, url string, result interface{}) (err error) {
	ctx, span := l.reg.Tracer(ctx).Tracer().Start(ctx, "selfservice.strategy.oidc.ProviderLinkedIn.fetch")
	defer otelx.End(span, &err)

	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errors.WithStack(err)
	}

	res, err := client.Do(req)
	if err != nil {
		return errors.WithStack(err)
	}

	defer res.Body.Close()
	if err := logUpstreamError(l.reg.Logger(), res); err != nil {
		return err
	}

	if err := json.NewDecoder(res.Body).Decode(result); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (l *ProviderLinkedIn) Profile(ctx context.Context, client *retryablehttp.Client) (*LinkedInProfile, error) {
	var result LinkedInProfile

	if err := l.fetch(ctx, client, ProfileUrl, &result); err != nil {
		return nil, errors.WithStack(err)
	}

	return &result, nil
}

func (l *ProviderLinkedIn) ProfileLocale(profile *LinkedInProfile) string {
	if profile.Locale == nil {
		return ""
	}
	return profile.Locale.Lanuguage
}

func (l *ProviderLinkedIn) Claims(ctx context.Context, exchange *oauth2.Token, query url.Values) (_ *Claims, err error) {
	ctx, span := l.reg.Tracer(ctx).Tracer().Start(ctx, "selfservice.strategy.oidc.ProviderLinkedIn.Claims")
	defer otelx.End(span, &err)

	o, err := l.OAuth2(ctx)
	if err != nil {
		return nil, errors.WithStack(herodot.ErrInternalServerError.WithReasonf("%s", err))
	}

	ctx, client := httpx.SetOAuth2(ctx, l.reg.HTTPClient(ctx), o, exchange)
	profile, err := l.Profile(ctx, client)
	if err != nil {
		return nil, errors.WithStack(herodot.ErrInternalServerError.WithReasonf("%s", err))
	}

	claims := &Claims{
		Subject:       profile.ID,
		Issuer:        "https://login.linkedin.com/",
		Email:         profile.EmailAddress,
		GivenName:     profile.LocalizedFirstName,
		LastName:      profile.LocalizedLastName,
		Picture:       profile.ProfilePicture,
		EmailVerified: x.ConvertibleBoolean(profile.EmailVerified),
		Locale:        l.ProfileLocale(profile),
	}

	return claims, nil
}
