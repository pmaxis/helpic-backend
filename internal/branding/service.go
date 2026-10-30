package branding

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // logos are PNG or JPEG
	_ "image/png"
	"io"
	"net/http"
)

// logoTypes are the images a logo may be: the ones a PDF embeds as they are.
var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true}

// Service holds the rules for the look of a freelancer's reports.
type Service struct {
	repo *Repository
}

// NewService creates a branding Service.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// Get returns the user's look.
func (s *Service) Get(ctx context.Context, userID string) (Branding, error) {
	return s.repo.Get(ctx, userID)
}

// Set changes the user's color and business details.
func (s *Service) Set(ctx context.Context, userID string, p Params) (Branding, error) {
	if err := s.repo.Set(ctx, userID, p); err != nil {
		return Branding{}, err
	}
	return s.repo.Get(ctx, userID)
}

// SetLogo makes the image read from r the user's logo. It must be a whole
// PNG or JPEG, its type read from the bytes, of a sensible size.
func (s *Service) SetLogo(ctx context.Context, userID string, r io.Reader) (Branding, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxLogoBytes+1))
	if err != nil {
		return Branding{}, fmt.Errorf("read logo: %w", err)
	}
	if len(data) > MaxLogoBytes {
		return Branding{}, ErrTooLarge
	}
	contentType, err := checkImage(data)
	if err != nil {
		return Branding{}, err
	}
	if err := s.repo.SetLogo(ctx, userID, contentType, data); err != nil {
		return Branding{}, err
	}
	return s.repo.Get(ctx, userID)
}

// checkImage returns the image's type, once its size is known to be sensible
// and the whole of it decodes: a broken file would break the PDF it's in.
func checkImage(data []byte) (string, error) {
	contentType := http.DetectContentType(data)
	if !logoTypes[contentType] {
		return "", ErrNotImage
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", ErrNotImage
	}
	// Checked before decoding, so a small file can't claim a huge picture.
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxLogoSide || cfg.Height > MaxLogoSide {
		return "", ErrTooBig
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", ErrNotImage
	}
	return contentType, nil
}

// RemoveLogo takes the user's logo away. Reports they sent keep theirs.
func (s *Service) RemoveLogo(ctx context.Context, userID string) (Branding, error) {
	if err := s.repo.SetLogo(ctx, userID, "", nil); err != nil {
		return Branding{}, err
	}
	return s.repo.Get(ctx, userID)
}

// OwnLogo returns the user's current logo, or ErrNotFound without one.
func (s *Service) OwnLogo(ctx context.Context, userID string) (Logo, error) {
	b, err := s.repo.Get(ctx, userID)
	if err != nil {
		return Logo{}, err
	}
	if b.LogoID == "" {
		return Logo{}, ErrNotFound
	}
	return s.repo.Logo(ctx, b.LogoID)
}
