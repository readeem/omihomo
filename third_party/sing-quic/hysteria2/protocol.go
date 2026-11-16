package hysteria2

import (
	"time"
)

const (
	DefaultStreamReceiveWindow = 8388608                            // 8MB
	DefaultConnReceiveWindow   = DefaultStreamReceiveWindow * 5 / 2 // 20MB
	DefaultMaxIdleTimeout      = 30 * time.Second
	DefaultKeepAlivePeriod     = 10 * time.Second
)
