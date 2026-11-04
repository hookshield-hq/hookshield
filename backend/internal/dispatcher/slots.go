package dispatcher

import "sync"

// EndpointSlots caps the deliveries in flight per endpoint. Share one value
// between the dispatchers of a worker process so a slow or black-hole
// destination can hold at most Max delivery slots of that worker, whatever
// the number of dispatchers: the other endpoints keep flowing.
type EndpointSlots struct {
	max  int
	mu   sync.Mutex
	used map[string]int
}

// DefaultMaxPerEndpoint is the default cap of in-flight deliveries per endpoint.
const DefaultMaxPerEndpoint = 4

// NewEndpointSlots returns a limiter allowing max deliveries per endpoint
// (DefaultMaxPerEndpoint when max <= 0).
func NewEndpointSlots(max int) *EndpointSlots {
	if max <= 0 {
		max = DefaultMaxPerEndpoint
	}
	return &EndpointSlots{max: max, used: map[string]int{}}
}

// TryAcquire takes a slot for endpointID without waiting; false means the
// endpoint already has Max deliveries in flight.
func (s *EndpointSlots) TryAcquire(endpointID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[endpointID] >= s.max {
		return false
	}
	s.used[endpointID]++
	return true
}

// Release returns a slot taken by TryAcquire.
func (s *EndpointSlots) Release(endpointID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[endpointID] <= 1 {
		delete(s.used, endpointID) // keep the map as small as the in-flight set
		return
	}
	s.used[endpointID]--
}
