package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/ratelimit"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// CookieConfig describes how the auth cookies are written. Values come from
// environment configuration so development and production can differ.
type CookieConfig struct {
	// Name and Path are the access token's cookie, sent with every request.
	Name string
	Path string
	// RefreshName and RefreshPath are the refresh token's cookie, sent only
	// to the auth endpoints.
	RefreshName string
	RefreshPath string
	Secure      bool
	SameSite    http.SameSite
}

// Handler exposes the authentication endpoints over HTTP. It owns request
// parsing, validation and response shaping only — no business logic.
type Handler struct {
	svc    *Service
	cookie CookieConfig
	// failures counts wrong passwords per email from one address, and
	// emailFailures per email from anywhere; nil doesn't limit them.
	failures, emailFailures ratelimit.Gate
	// resets counts reset links asked for per email from one address, and
	// emailResets per email from anywhere; nil doesn't limit them.
	resets, emailResets ratelimit.Gate
}

// NewHandler creates an auth Handler.
func NewHandler(svc *Service, cookie CookieConfig) *Handler {
	return &Handler{svc: svc, cookie: cookie}
}

// LimitResetRequests stops emailing reset links to an address once either
// gate runs out: perClient counts one sender's requests for the address,
// perEmail everyone's, with room enough that a stranger running it down
// takes a long, steady effort. The answer stays the same, so it tells
// nothing and floods no inbox.
func (h *Handler) LimitResetRequests(perClient, perEmail ratelimit.Gate) {
	h.resets, h.emailResets = perClient, perEmail
}

// LimitFailedLogins refuses sign-ins to an email once either gate runs out:
// each wrong password takes a token from both, a right one takes none.
// perClient is per email and address, so guessing from one place doesn't
// lock the owner out; perEmail caps guessing from many places at once.
func (h *Handler) LimitFailedLogins(perClient, perEmail ratelimit.Gate) {
	h.failures, h.emailFailures = perClient, perEmail
}

// clientKey is a bucket for one email from one address.
func clientKey(email string, r *http.Request) string {
	return email + " " + middleware.ClientIP(r)
}

// checkAll reports whether every gate has a token for its key, and if not
// how long until they do. Nil gates don't limit.
func checkAll(r *http.Request, gates []ratelimit.Gate, keys []string) (bool, time.Duration, error) {
	for i, g := range gates {
		if g == nil {
			continue
		}
		ok, retry, err := g.Check(r.Context(), keys[i])
		if err != nil || !ok {
			return ok, retry, err
		}
	}
	return true, 0, nil
}

// takeAll uses a token of every gate for its key.
func takeAll(r *http.Request, gates []ratelimit.Gate, keys []string) error {
	for i, g := range gates {
		if g == nil {
			continue
		}
		if err := g.Take(r.Context(), keys[i]); err != nil {
			return err
		}
	}
	return nil
}

// Register handles POST /api/v1/auth/register.
//
//	@Summary		Sign up as a freelancer
//	@Description	Emails a confirmation link. The account is created when that link is opened, not before. An email that already has an account gets a note saying so instead; the answer is the same, so it doesn't tell who has an account.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		RegisterRequest			true	"Request body"
//	@Success		202		{object}	map[string]string		"Confirmation emailed; no account yet"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many sign-ups from this address"
//	@Router			/auth/register [post]
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	params, fieldErrs := req.Validate()
	if len(fieldErrs) > 0 {
		response.ValidationError(w, fieldErrs)
		return
	}

	if err := h.svc.RequestSignup(r.Context(), params); err != nil {
		response.Internal(w, err)
		return
	}

	response.JSON(w, http.StatusAccepted, map[string]string{"email": params.Email})
}

// ConfirmSignup handles POST /api/v1/auth/signups/confirm.
//
//	@Summary		Confirm a sign-up email
//	@Description	Creates the freelancer account and signs this browser in. The link is the proof the inbox is theirs.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		SignupConfirmRequest	true	"Request body"
//	@Success		201		{object}	user.Response			"The new account, signed in"
//	@Failure		400		{object}	response.ErrorEnvelope	"The link is invalid, replaced, used or expired (field token)"
//	@Failure		409		{object}	response.ErrorEnvelope	"The email was taken before the link was opened"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address"
//	@Router			/auth/signups/confirm [post]
func (h *Handler) ConfirmSignup(w http.ResponseWriter, r *http.Request) {
	var req SignupConfirmRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		response.ValidationError(w, map[string]string{"token": "This link has expired or was already used. Ask for a new one."})
		return
	}

	u, tokens, err := h.svc.ConfirmSignup(r.Context(), req.Token, ClientOf(r))
	if errors.Is(err, ErrSignupInvalid) {
		response.ValidationError(w, map[string]string{"token": "This link has expired or was already used. Ask for a new one."})
		return
	}
	if errors.Is(err, ErrEmailExists) {
		response.Error(w, http.StatusConflict, response.CodeEmailExists, "An account with this email already exists")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	h.cookie.Set(w, tokens)
	response.JSON(w, http.StatusCreated, user.NewResponse(u))
}

// Login handles POST /api/v1/auth/login.
//
//	@Summary		Sign in
//	@Description	Sets the httpOnly auth cookie that every other endpoint reads. An account with two-factor on answers a right password without a code with 403 TWO_FACTOR_REQUIRED; send the same again with the code.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LoginRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input, or a wrong code (field code)"
//	@Failure		401		{object}	response.ErrorEnvelope	"Wrong email or password"
//	@Failure		403		{object}	response.ErrorEnvelope	"The password is right; the account also needs a code (TWO_FACTOR_REQUIRED)"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address, wrong passwords for this email, or wrong codes for this account"
//	@Router			/auth/login [post]
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	params, fieldErrs := req.Validate()
	if len(fieldErrs) > 0 {
		response.ValidationError(w, fieldErrs)
		return
	}

	gates := []ratelimit.Gate{h.failures, h.emailFailures}
	keys := []string{clientKey(params.Email, r), params.Email}
	ok, retry, err := checkAll(r, gates, keys)
	if err != nil {
		response.Internal(w, err)
		return
	}
	if !ok {
		ratelimit.TooMany(w, retry)
		return
	}

	u, tokens, err := h.svc.Login(r.Context(), params, ClientOf(r))
	if errors.Is(err, ErrInvalidCredentials) {
		if err := takeAll(r, gates, keys); err != nil {
			response.Internal(w, err)
			return
		}
		response.Error(w, http.StatusUnauthorized, response.CodeInvalidCredential, "Invalid email or password")
		return
	}
	if WriteTwoFactorError(w, err) {
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	h.cookie.Set(w, tokens)
	response.JSON(w, http.StatusOK, user.NewResponse(u))
}

// Refresh handles POST /api/v1/auth/refresh.
//
//	@Summary		Refresh the session
//	@Description	Swaps the refresh cookie for a new one and sets a new access cookie. A refresh token used again after it was replaced ends the session.
//	@Tags			auth
//	@Success		204	"Refreshed"
//	@Failure		401	{object}	response.ErrorEnvelope	"No session, or it ended"
//	@Failure		429	{object}	response.ErrorEnvelope	"Too many refreshes from this address"
//	@Router			/auth/refresh [post]
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.svc.Refresh(r.Context(), h.cookie.refreshToken(r))
	if errors.Is(err, ErrSessionInvalid) {
		h.cookie.Clear(w)
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Your session has ended")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	h.cookie.Set(w, tokens)
	w.WriteHeader(http.StatusNoContent)
}

// Logout handles POST /api/v1/auth/logout. It ends the session and clears the
// auth cookies.
//
//	@Summary		Sign out
//	@Description	Ends this device's session and clears the auth cookies.
//	@Tags			auth
//	@Success		204	"Signed out"
//	@Router			/auth/logout [post]
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Logout(r.Context(), h.cookie.refreshToken(r)); err != nil {
		response.Internal(w, err)
		return
	}
	h.cookie.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// Forgot handles POST /api/v1/auth/password/forgot.
//
//	@Summary		Ask for a password reset link
//	@Description	Emails a one-time link to the account with this email. The answer is the same whether there is one or not.
//	@Tags			auth
//	@Accept			json
//	@Param			body	body	ForgotRequest	true	"Request body"
//	@Success		204		"Sent, if there is such an account"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid email"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address"
//	@Router			/auth/password/forgot [post]
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	var req ForgotRequest
	if !request.Decode(w, r, &req) {
		return
	}
	email, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	gates := []ratelimit.Gate{h.resets, h.emailResets}
	keys := []string{clientKey(email, r), email}
	ok, _, err := checkAll(r, gates, keys)
	if err != nil {
		response.Internal(w, err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := takeAll(r, gates, keys); err != nil {
		response.Internal(w, err)
		return
	}
	if err := h.svc.RequestReset(r.Context(), email); err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reset handles POST /api/v1/auth/password/reset.
//
//	@Summary		Set a new password with a reset link
//	@Description	Signs out every device; sign in with the new password.
//	@Tags			auth
//	@Accept			json
//	@Param			body	body	ResetRequest	true	"Request body"
//	@Success		204		"Password set"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid password, or the link is invalid, used or expired (field token)"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address"
//	@Router			/auth/password/reset [post]
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	err := h.svc.ResetPassword(r.Context(), req.Token, req.Password)
	if errors.Is(err, ErrResetInvalid) {
		response.ValidationError(w, map[string]string{"token": "This link has expired or was already used. Ask for a new one."})
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SessionRoutes mounts the endpoints for the user's signed-in devices. Must be
// called inside an authenticated group.
func (h *Handler) SessionRoutes(r chi.Router) {
	r.Route("/me/sessions", func(r chi.Router) {
		r.Get("/", h.Sessions)
		r.Delete("/", h.EndOtherSessions)
		r.Delete("/{id}", h.EndSession)
	})
}

// Sessions handles GET /api/v1/me/sessions.
//
//	@Summary	Your signed-in devices
//	@Tags		account
//	@Produce	json
//	@Success	200	{object}	map[string][]SessionDTO	"sessions, the most recently used first"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/me/sessions [get]
func (h *Handler) Sessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	list, err := h.svc.Sessions(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	current := middleware.SessionID(r.Context())
	out := make([]SessionDTO, 0, len(list))
	for _, sess := range list {
		out = append(out, ToSessionDTO(sess, current))
	}
	response.JSON(w, http.StatusOK, map[string][]SessionDTO{"sessions": out})
}

// EndSession handles DELETE /api/v1/me/sessions/{id}.
//
//	@Summary		Sign a device out
//	@Description	Its access token works until it expires, minutes at most; it can't be refreshed.
//	@Tags			account
//	@Param			id	path	string	true	"Session ID"
//	@Success		204	"Signed out"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404	{object}	response.ErrorEnvelope	"No such session"
//	@Router			/me/sessions/{id} [delete]
func (h *Handler) EndSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	err := h.svc.EndSession(r.Context(), userID, chi.URLParam(r, "id"))
	if errors.Is(err, ErrSessionNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Session not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// EndOtherSessions handles DELETE /api/v1/me/sessions.
//
//	@Summary	Sign out every other device
//	@Tags		account
//	@Success	204	"Signed out"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/me/sessions [delete]
func (h *Handler) EndOtherSessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	if err := h.svc.EndOtherSessions(r.Context(), userID, middleware.SessionID(r.Context())); err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// WriteTwoFactorError answers for a second factor that is missing or wrong,
// and reports whether err was one. Every way of signing in with a password
// shares it: a missing code is 403 TWO_FACTOR_REQUIRED (not 401, which means
// a session ended), a wrong one is the field "code", too many are 429.
func WriteTwoFactorError(w http.ResponseWriter, err error) bool {
	var limit *CodeLimitError
	switch {
	case errors.Is(err, ErrTwoFactorRequired):
		response.Error(w, http.StatusForbidden, response.CodeTwoFactorRequired, "Enter the code from your authenticator app")
	case errors.Is(err, ErrTwoFactorCode):
		response.ValidationError(w, map[string]string{"code": "That code didn't work. Check that your phone's clock is right, or use a recovery code."})
	case errors.As(err, &limit):
		ratelimit.TooMany(w, limit.Retry)
	default:
		return false
	}
	return true
}

// TwoFactorRoutes mounts the endpoints for the user's two-factor sign-in.
// Must be called inside an authenticated group; guard wraps the ones that
// check the current password, to limit guessing.
func (h *Handler) TwoFactorRoutes(r chi.Router, guard func(http.Handler) http.Handler) {
	r.Route("/me/two-factor", func(r chi.Router) {
		r.Get("/", h.TwoFactor)
		r.With(guard).Post("/setup", h.BeginTwoFactor)
		r.Post("/enable", h.ConfirmTwoFactor)
		r.With(guard).Post("/disable", h.DisableTwoFactor)
		r.With(guard).Post("/recovery-codes", h.RenewRecoveryCodes)
	})
}

// TwoFactor handles GET /api/v1/me/two-factor.
//
//	@Summary	Your two-factor sign-in
//	@Tags		account
//	@Produce	json
//	@Success	200	{object}	TwoFactorDTO			"OK"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/me/two-factor [get]
func (h *Handler) TwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	st, err := h.svc.TwoFactor(r.Context(), userID)
	if err != nil {
		writeTwoFactorChange(w, err)
		return
	}
	response.JSON(w, http.StatusOK, TwoFactorDTO{Enabled: st.Enabled, RecoveryCodesLeft: st.RecoveryCodesLeft})
}

// BeginTwoFactor handles POST /api/v1/me/two-factor/setup.
//
//	@Summary		Start turning on two-factor sign-in
//	@Description	Needs the current password. Returns a new secret for an authenticator app, as a QR code and as text; it is used once a code from it confirms it (POST /me/two-factor/enable). Starting again replaces it.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		PasswordConfirmRequest	true	"Request body"
//	@Success		200		{object}	TwoFactorSetupDTO		"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Wrong password"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		409		{object}	response.ErrorEnvelope	"Two-factor is on already"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts"
//	@Router			/me/two-factor/setup [post]
func (h *Handler) BeginTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req PasswordConfirmRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	setup, err := h.svc.BeginTwoFactor(r.Context(), userID, req.CurrentPassword)
	if err != nil {
		writeTwoFactorChange(w, err)
		return
	}
	response.JSON(w, http.StatusOK, TwoFactorSetupDTO{Secret: setup.Secret, URI: setup.URI, QR: setup.QR})
}

// ConfirmTwoFactor handles POST /api/v1/me/two-factor/enable.
//
//	@Summary		Turn on two-factor sign-in
//	@Description	With a code from the app the setup's secret went into. Signs out every other device and returns the recovery codes, shown this once.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		TwoFactorCodeRequest	true	"Request body"
//	@Success		200		{object}	TwoFactorOnDTO			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Wrong code (field code)"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		409		{object}	response.ErrorEnvelope	"No setup was started, or it was started again elsewhere"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many wrong codes"
//	@Router			/me/two-factor/enable [post]
func (h *Handler) ConfirmTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req TwoFactorCodeRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	u, codes, err := h.svc.ConfirmTwoFactor(r.Context(), userID, middleware.SessionID(r.Context()), req.Code)
	if err != nil {
		writeTwoFactorChange(w, err)
		return
	}
	response.JSON(w, http.StatusOK, TwoFactorOnDTO{User: user.ToDTO(u), RecoveryCodes: codes})
}

// DisableTwoFactor handles POST /api/v1/me/two-factor/disable.
//
//	@Summary		Turn off two-factor sign-in
//	@Description	Needs the current password and a code from the app or a recovery code.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		TwoFactorChangeRequest	true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Wrong password or code"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		409		{object}	response.ErrorEnvelope	"Two-factor is off"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts"
//	@Router			/me/two-factor/disable [post]
func (h *Handler) DisableTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req TwoFactorChangeRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	u, err := h.svc.DisableTwoFactor(r.Context(), userID, req.CurrentPassword, req.Code)
	if err != nil {
		writeTwoFactorChange(w, err)
		return
	}
	response.JSON(w, http.StatusOK, user.NewResponse(u))
}

// RenewRecoveryCodes handles POST /api/v1/me/two-factor/recovery-codes.
//
//	@Summary		Make new recovery codes
//	@Description	Needs the current password and a code from the app or a recovery code. The old codes stop working; the new ones are shown this once.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		TwoFactorChangeRequest	true	"Request body"
//	@Success		200		{object}	RecoveryCodesDTO		"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Wrong password or code"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		409		{object}	response.ErrorEnvelope	"Two-factor is off"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts"
//	@Router			/me/two-factor/recovery-codes [post]
func (h *Handler) RenewRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req TwoFactorChangeRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	codes, err := h.svc.RenewRecoveryCodes(r.Context(), userID, req.CurrentPassword, req.Code)
	if err != nil {
		writeTwoFactorChange(w, err)
		return
	}
	response.JSON(w, http.StatusOK, RecoveryCodesDTO{RecoveryCodes: codes})
}

// writeTwoFactorChange answers for a failed change to two-factor sign-in.
func writeTwoFactorChange(w http.ResponseWriter, err error) {
	if WriteTwoFactorError(w, err) {
		return
	}
	switch {
	case errors.Is(err, ErrWrongPassword):
		response.ValidationError(w, map[string]string{"current_password": "Wrong password"})
	case errors.Is(err, ErrTwoFactorOn):
		response.Error(w, http.StatusConflict, response.CodeConflict, "Two-factor sign-in is already on")
	case errors.Is(err, ErrTwoFactorOff):
		response.Error(w, http.StatusConflict, response.CodeConflict, "Two-factor sign-in is off")
	case errors.Is(err, ErrTwoFactorNotStarted):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This setup has expired. Start turning on two-factor sign-in again.")
	case errors.Is(err, user.ErrNotFound):
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
	default:
		response.Internal(w, err)
	}
}

// Set writes the access cookie and, when the tokens carry one, the refresh
// cookie.
func (c CookieConfig) Set(w http.ResponseWriter, t Tokens) {
	c.write(w, c.Name, c.Path, t.Access, t.AccessExpires)
	if t.Refresh != "" {
		c.write(w, c.RefreshName, c.RefreshPath, t.Refresh, t.RefreshExpires)
	}
}

// Clear expires both auth cookies.
func (c CookieConfig) Clear(w http.ResponseWriter) {
	c.write(w, c.Name, c.Path, "", time.Time{})
	c.write(w, c.RefreshName, c.RefreshPath, "", time.Time{})
}

// write sets an httpOnly cookie; a zero expiry expires it.
func (c CookieConfig) write(w http.ResponseWriter, name, path, value string, expiresAt time.Time) {
	maxAge := -1
	if expiresAt.IsZero() {
		expiresAt = time.Unix(0, 0)
	} else {
		maxAge = int(time.Until(expiresAt).Seconds())
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: c.SameSite,
		Expires:  expiresAt,
		MaxAge:   maxAge,
	})
}

func (c CookieConfig) refreshToken(r *http.Request) string {
	ck, err := r.Cookie(c.RefreshName)
	if err != nil {
		return ""
	}
	return ck.Value
}

// decodeJSON strictly decodes the request body, writing a 400 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidation, "Request body is not valid JSON")
		return false
	}
	return true
}
