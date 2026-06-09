package main

import (
	"net/http"
	"time"
)

const (
	sessionCookieName = "valet_session"
	sessionTTL        = 12 * time.Hour
	csrfFormField     = "csrfToken"
)

type session struct {
	Email     string
	CSRFToken string
	CreatedAt time.Time
}

func (s *accountStore) startSession(w http.ResponseWriter, r *http.Request, email string) error {
	sessionID, err := randomState()
	if err != nil {
		return err
	}
	csrfToken, err := randomState()
	if err != nil {
		return err
	}

	now := time.Now()
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]session{}
	}
	if oldCookie, err := r.Cookie(sessionCookieName); err == nil {
		delete(s.sessions, oldCookie.Value)
	}
	s.sessions[sessionID] = session{
		Email:     email,
		CSRFToken: csrfToken,
		CreatedAt: now,
	}
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

func (s *accountStore) currentSession(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return session{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.sessions[cookie.Value]
	if !ok {
		return session{}, false
	}
	if time.Since(current.CreatedAt) > sessionTTL {
		delete(s.sessions, cookie.Value)
		return session{}, false
	}
	return current, true
}

func (s *accountStore) endSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
		MaxAge:   -1,
	})
}

func (s *accountStore) validateCSRF(r *http.Request) bool {
	current, ok := s.currentSession(r)
	if !ok {
		return false
	}
	return r.FormValue(csrfFormField) == current.CSRFToken
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
