package backend

// VetoCache remembers verifier overrides of DONE and BLOCKED; one cache belongs to one run and one goroutine.
type VetoCache struct {
	entries map[VetoKey]vetoEntry
}

type vetoEntry struct {
	res  *response
	step int
}

// NewVetoCache returns an empty cache.
func NewVetoCache() *VetoCache {
	return &VetoCache{entries: map[VetoKey]vetoEntry{}}
}

// Len returns the number of stored overrides.
func (c *VetoCache) Len() int { return len(c.entries) }

// Forget drops the entry for key; call it after an action changed the page.
func (c *VetoCache) Forget(key VetoKey) { delete(c.entries, key) }

func (c *VetoCache) store(key VetoKey, res *response, step int) {
	if step <= 0 {
		step = len(c.entries) + 1
	}
	c.entries[key] = vetoEntry{res: res, step: step}
}

func (c *VetoCache) lookup(key VetoKey, sp *space) (vetoEntry, fields, bool) {
	entry, ok := c.entries[key]
	if !ok {
		return vetoEntry{}, fields{}, false
	}
	verdict, err := interpret(entry.res, sp)
	if err != nil {
		delete(c.entries, key)
		return vetoEntry{}, fields{}, false
	}
	return entry, verdict, true
}
