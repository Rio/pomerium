package sessions // import "github.com/pomerium/pomerium/internal/sessions"

import (
	"errors"
	"fmt"
	"strings"
	"time"

	oidc "github.com/pomerium/go-oidc"
	"golang.org/x/oauth2"
	"gopkg.in/square/go-jose.v2/jwt"
)

const (
	// DefaultLeeway defines the default leeway for matching NotBefore/Expiry claims.
	DefaultLeeway = 1.0 * time.Minute
)

// timeNow is time.Now but pulled out as a variable for tests.
var timeNow = time.Now

// State is our object that keeps track of a user's session state
type State struct {
	// Public claim values (as specified in RFC 7519).
	Issuer    string           `json:"iss,omitempty"`
	Subject   string           `json:"sub,omitempty"`
	Audience  jwt.Audience     `json:"aud,omitempty"`
	Expiry    *jwt.NumericDate `json:"exp,omitempty"`
	NotBefore *jwt.NumericDate `json:"nbf,omitempty"`
	IssuedAt  *jwt.NumericDate `json:"iat,omitempty"`
	ID        string           `json:"jti,omitempty"`

	// core pomerium identity claims ; not standard to RFC 7519
	Email  string   `json:"email"`
	Groups []string `json:"groups,omitempty"`
	User   string   `json:"user,omitempty"` // google

	// commonly supported IdP information
	// https://www.iana.org/assignments/jwt/jwt.xhtml#claims
	Name          string `json:"name,omitempty"`           // google
	GivenName     string `json:"given_name,omitempty"`     // google
	FamilyName    string `json:"family_name,omitempty"`    // google
	Picture       string `json:"picture,omitempty"`        // google
	EmailVerified bool   `json:"email_verified,omitempty"` // google

	// Impersonate-able fields
	ImpersonateEmail  string   `json:"impersonate_email,omitempty"`
	ImpersonateGroups []string `json:"impersonate_groups,omitempty"`

	// Programmatic whether this state is used for machine-to-machine
	// programatic access.
	Programmatic bool `json:"programatic"`

	AccessToken *oauth2.Token `json:"access_token,omitempty"`

	idToken *oidc.IDToken
}

// NewStateFromTokens returns a session state built from oidc and oauth2
// tokens as part of OpenID Connect flow with a new audience appended to the
// audience claim.
func NewStateFromTokens(idToken *oidc.IDToken, accessToken *oauth2.Token, audience string) (*State, error) {
	if idToken == nil {
		return nil, errors.New("sessions: oidc id token missing")
	}
	if accessToken == nil {
		return nil, errors.New("sessions: oauth2 token missing")
	}
	s := &State{}
	if err := idToken.Claims(s); err != nil {
		return nil, fmt.Errorf("sessions: couldn't unmarshal extra claims %w", err)
	}
	s.Audience = []string{audience}
	s.idToken = idToken
	s.AccessToken = accessToken

	return s, nil
}

// UpdateState updates the current state given a new identity (oidc) and authorization
// (oauth2) tokens following a oidc refresh. NB, unlike during authentication,
// refresh typically provides fewer claims in the token so we want to build from
// our previous state.
func (s *State) UpdateState(idToken *oidc.IDToken, accessToken *oauth2.Token) error {
	if idToken == nil {
		return errors.New("sessions: oidc id token missing")
	}
	if accessToken == nil {
		return errors.New("sessions: oauth2 token missing")
	}
	audience := append(s.Audience[:0:0], s.Audience...)
	s.AccessToken = accessToken
	if err := idToken.Claims(s); err != nil {
		return fmt.Errorf("sessions: update state failed %w", err)
	}
	s.Audience = audience
	s.Expiry = jwt.NewNumericDate(accessToken.Expiry)
	return nil
}

// NewSession updates issuer, audience, and issuance timestamps but keeps
// parent expiry.
func (s State) NewSession(issuer string, audience []string) *State {
	s.IssuedAt = jwt.NewNumericDate(timeNow())
	s.NotBefore = s.IssuedAt
	s.Audience = audience
	s.Issuer = issuer
	return &s
}

// RouteSession creates a route session with access tokens stripped.
func (s State) RouteSession() *State {
	s.AccessToken = nil
	return &s
}

// Verify returns an error if the users's session state is not valid.
func (s *State) Verify(audience string) error {
	if s.NotBefore != nil && timeNow().Add(DefaultLeeway).Before(s.NotBefore.Time()) {
		return ErrNotValidYet
	}

	if s.Expiry != nil && timeNow().Add(-DefaultLeeway).After(s.Expiry.Time()) {
		return ErrExpired
	}

	if s.IssuedAt != nil && timeNow().Add(DefaultLeeway).Before(s.IssuedAt.Time()) {
		return ErrIssuedInTheFuture
	}

	// if we have an associated access token, check if that token has expired as well
	if s.AccessToken != nil && timeNow().Add(-DefaultLeeway).After(s.AccessToken.Expiry) {
		return ErrExpired
	}

	if len(s.Audience) != 0 {
		if !s.Audience.Contains(audience) {
			return ErrInvalidAudience
		}

	}
	return nil
}

// Impersonating returns if the request is impersonating.
func (s *State) Impersonating() bool {
	return s.ImpersonateEmail != "" || len(s.ImpersonateGroups) != 0
}

// RequestEmail is the email to make the request as.
func (s *State) RequestEmail() string {
	if s.ImpersonateEmail != "" {
		return s.ImpersonateEmail
	}
	return s.Email
}

// RequestGroups returns the groups of the Groups making the request; uses
// impersonating user if set.
func (s *State) RequestGroups() string {
	if len(s.ImpersonateGroups) != 0 {
		return strings.Join(s.ImpersonateGroups, ",")
	}
	return strings.Join(s.Groups, ",")
}

// SetImpersonation sets impersonation user and groups.
func (s *State) SetImpersonation(email, groups string) {
	s.ImpersonateEmail = email
	if groups == "" {
		s.ImpersonateGroups = nil
	} else {
		s.ImpersonateGroups = strings.Split(groups, ",")
	}
}
