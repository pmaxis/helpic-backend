package telegram

import "time"

// StatusDTO is the user's Telegram connection as the API shows it.
type StatusDTO struct {
	// Enabled is false when the server has no bot.
	Enabled   bool `json:"enabled"`
	Connected bool `json:"connected"`
	// Username is the connected Telegram account's @handle, or its first name.
	Username string     `json:"username"`
	Kinds    []string   `json:"kinds"`
	LinkedAt *time.Time `json:"linked_at"`
	// Available are the kinds that can go to Telegram; DefaultKinds those on
	// when a chat is connected.
	Available    []string `json:"available"`
	DefaultKinds []string `json:"default_kinds"`
}

// ToStatusDTO converts a Status.
func ToStatusDTO(s Status) StatusDTO {
	out := StatusDTO{Enabled: s.Enabled, Kinds: []string{}, Available: Kinds, DefaultKinds: DefaultKinds}
	if s.Link != nil {
		out.Connected = true
		out.Username = s.Link.Username
		out.Kinds = s.Link.Kinds
		out.LinkedAt = &s.Link.LinkedAt
	}
	return out
}

// ConnectDTO is the one-time link that connects a chat.
type ConnectDTO struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// KindsRequest is the JSON body for PUT /api/v1/me/telegram/kinds.
type KindsRequest struct {
	Kinds []string `json:"kinds"`
}
