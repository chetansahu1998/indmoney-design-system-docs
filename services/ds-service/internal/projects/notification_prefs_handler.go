package projects

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// Phase 7.5 — notification preferences CRUD HTTP layer.
//
// The repo (digest.go) shipped UpsertNotificationPreference +
// GetNotificationPreference at Phase 5. Phase 5 closure flagged the
// user-facing settings surface as deferred; this handler closes that.
//
// Routes:
//   GET /v1/users/me/notification-preferences        → list both channels
//   PUT /v1/users/me/notification-preferences        → upsert one channel

// NotificationPreferencesView is the shape /v1/users/me/notification-preferences
// returns. Always two entries (slack + email); both default to cadence=off
// when no row exists yet so the frontend renders the form without a special-
// case "no prefs" branch.
type NotificationPreferencesView struct {
	Slack PreferenceRecord `json:"slack"`
	Email PreferenceRecord `json:"email"`
}

// HandleListMyNotificationPrefs serves GET. Returns the two channel rows
// for the authed user. Missing rows are filled with cadence=off defaults
// so the form is always renderable.
func (s *Server) HandleListMyNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	userID := claims.Sub

	view := NotificationPreferencesView{
		Slack: PreferenceRecord{UserID: userID, Channel: ChannelSlack, Cadence: CadenceOff},
		Email: PreferenceRecord{UserID: userID, Channel: ChannelEmail, Cadence: CadenceOff},
	}
	if rec, err := NewDB(s.deps.DB.DB).GetNotificationPreference(r.Context(), userID, ChannelSlack); err == nil && rec != nil {
		view.Slack = *rec
	}
	if rec, err := NewDB(s.deps.DB.DB).GetNotificationPreference(r.Context(), userID, ChannelEmail); err == nil && rec != nil {
		view.Email = *rec
	}
	writeJSON(w, http.StatusOK, view)
}

// HandleUpsertMyNotificationPref serves PUT. Body shape mirrors
// PreferenceInput. The handler enforces user_id == claims.sub regardless
// of body content — users can only edit their own prefs.
func (s *Server) HandleUpsertMyNotificationPref(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	var body struct {
		Channel         string `json:"channel"`
		Cadence         string `json:"cadence"`
		SlackWebhookURL string `json:"slack_webhook_url,omitempty"`
		EmailAddress    string `json:"email_address,omitempty"`
		UserTZ          string `json:"user_tz,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	in := PreferenceInput{
		UserID:          claims.Sub, // forced — never trust body's user_id
		Channel:         body.Channel,
		Cadence:         body.Cadence,
		SlackWebhookURL: body.SlackWebhookURL,
		EmailAddress:    body.EmailAddress,
		UserTZ:          body.UserTZ,
	}
	validated, err := ValidatePreferenceInput(in)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	if err := NewDB(s.deps.DB.DB).UpsertNotificationPreference(r.Context(), validated); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "upsert_failed", err.Error())
		return
	}
	rec, _ := NewDB(s.deps.DB.DB).GetNotificationPreference(r.Context(), claims.Sub, validated.Channel)
	if rec == nil {
		// Should not happen — we just upserted — but guard the nil deref.
		writeJSONErr(w, http.StatusInternalServerError, "post_upsert_lookup", "")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// helper export so unused-import linter stays happy across compile
// targets.
var _ = errors.New
