package kb

import "time"

// Test helpers for retrySet (production code only asks waits / fail / clear / keep / chunk / shrink).

// get is key's failures.
func (r *retrySet) get(key string) (projectRetry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.m[key]
	return f, ok
}

// set replaces key's failures (a wait made over).
func (r *retrySet) set(key string, f projectRetry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]projectRetry{}
	}
	r.m[key] = f
}

// len is how many keys wait.
func (r *retrySet) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m)
}

// over makes every wait over.
func (r *retrySet) over() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, f := range r.m {
		f.at = time.Now().Add(-time.Second)
		r.m[k] = f
	}
}
