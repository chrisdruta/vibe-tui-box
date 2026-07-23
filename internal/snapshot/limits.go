package snapshot

// Limits bound one snapshot. Zero fields take defaults.
type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxDepth      int
}

func DefaultLimits() Limits {
	return Limits{
		MaxFiles:      10000,
		MaxFileBytes:  64 << 20,
		MaxTotalBytes: 256 << 20,
		MaxDepth:      32,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.MaxFileBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = d.MaxTotalBytes
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	return l
}
