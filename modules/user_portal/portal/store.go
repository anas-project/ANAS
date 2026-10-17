package main

import (
	"sync"
	"time"
)

type Session struct {
	ID, CSRF, DirectoryGUID string
	Authentication
	Anonymous bool
	Expires   time.Time
}

type Store struct {
	mu            sync.Mutex
	sessions      map[string]Session
	flows         map[string]Flow
	logoutIDs     map[string]time.Time
	confirmations map[string]Confirmation
}

func newStore() *Store {
	return &Store{sessions: map[string]Session{}, flows: map[string]Flow{}, logoutIDs: map[string]time.Time{}, confirmations: map[string]Confirmation{}}
}

func (s *Store) prune() {
	now := time.Now()
	for k, v := range s.sessions {
		if !v.Expires.After(now) {
			delete(s.sessions, k)
		}
	}
	for k, v := range s.flows {
		if !v.Expires.After(now) {
			delete(s.flows, k)
		}
	}
	for k, v := range s.logoutIDs {
		if !v.After(now) {
			delete(s.logoutIDs, k)
		}
	}
	for k, v := range s.confirmations {
		if !v.Expires.After(now) {
			delete(s.confirmations, k)
		}
	}
}

func (s *Store) add(v Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if len(s.sessions) >= 4096 {
		return false
	}
	s.sessions[v.ID] = v
	return true
}
func (s *Store) get(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[id]
	if ok && !v.Expires.After(time.Now()) {
		delete(s.sessions, id)
		ok = false
	}
	return v, ok
}
func (s *Store) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	for k, v := range s.confirmations {
		if v.Session == id {
			delete(s.confirmations, k)
		}
	}
	for k, v := range s.flows {
		if v.Session == id {
			delete(s.flows, k)
		}
	}
}

func (s *Store) addConfirmation(key string, v Confirmation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if len(s.confirmations) >= 4096 {
		return false
	}
	s.confirmations[key] = v
	return true
}
func (s *Store) consumeConfirmation(key string) (Confirmation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.confirmations[key]
	delete(s.confirmations, key)
	return v, ok && v.Expires.After(time.Now())
}
func (s *Store) addFlow(f Flow) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if len(s.flows) >= 4096 {
		return false
	}
	s.flows[f.State] = f
	return true
}
func (s *Store) consumeFlow(state string) (Flow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flows[state]
	delete(s.flows, state)
	return f, ok && f.Expires.After(time.Now())
}
func (s *Store) reauthenticate(id string, a Authentication) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[id]
	if !ok || !v.Expires.After(time.Now()) || v.Subject != a.Subject {
		return false
	}
	v.AuthTime = a.AuthTime
	s.sessions[id] = v
	return true
}
func (s *Store) revoke(subject, sid, jti string, expiry time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if _, ok := s.logoutIDs[jti]; ok {
		return false
	}
	if len(s.logoutIDs) >= 8192 {
		return false
	}
	s.logoutIDs[jti] = expiry
	for k, v := range s.sessions {
		if v.Anonymous {
			continue
		}
		if subject != "" && v.Subject != subject {
			continue
		}
		if sid != "" && v.SID != sid {
			continue
		}
		delete(s.sessions, k)
	}
	return true
}

type LimitBucket struct {
	count   int
	expires time.Time
}
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]LimitBucket
}

func newLimiter() *Limiter { return &Limiter{buckets: map[string]LimitBucket{}} }
func (l *Limiter) allow(account, ip string) bool {
	return l.take([]struct {
		key   string
		limit int
	}{{"global", 200}, {"ip:" + ip, 20}, {"account:" + account, 5}})
}

// A stable identity bucket joins username and UPN aliases after directory lookup.
// The network and submitted-label limits still run before that lookup.
func (l *Limiter) allowIdentity(anchor string) bool {
	return l.take([]struct {
		key   string
		limit int
	}{{"identity:" + anchor, 5}})
}

func (l *Limiter) take(limits []struct {
	key   string
	limit int
}) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, v := range l.buckets {
		if !v.expires.After(now) {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) > 20000 {
		return false
	}
	for _, v := range limits {
		if l.buckets[v.key].count >= v.limit {
			return false
		}
	}
	for _, v := range limits {
		b := l.buckets[v.key]
		if b.expires.IsZero() {
			b.expires = now.Add(5 * time.Minute)
		}
		b.count++
		l.buckets[v.key] = b
	}
	return true
}
