package safety

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
)

type ProcessIdentity struct {
	Host     string `json:"host"`
	Platform string `json:"platform"`
	PID      int    `json:"pid"`
	Started  string `json:"started"`
}

func ProcessHost() (string, error) {
	identity, err := localProcessHost()
	if err != nil || identity == "" {
		return "", errors.New("scope process host cannot be confirmed")
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:]), nil
}

func CurrentProcessIdentity() (*ProcessIdentity, error) {
	host, err := ProcessHost()
	if err != nil {
		return nil, err
	}
	pid := os.Getpid()
	started, stopped, err := inspectScopeProcess(pid)
	if err != nil || stopped || started == "" {
		return nil, errors.New("scope process identity cannot be confirmed")
	}
	return &ProcessIdentity{Host: host, Platform: runtime.GOOS, PID: pid, Started: started}, nil
}

func (process ProcessIdentity) Valid() bool {
	_, err := hex.DecodeString(process.Host)
	if err != nil || len(process.Host) != 64 || process.PID <= 0 {
		return false
	}
	started := process.Started
	switch process.Platform {
	case "windows":
		if uint64(process.PID) > uint64(^uint32(0)) {
			return false
		}
	case "linux":
		if process.PID > 1<<31-1 {
			return false
		}
		parts := strings.Split(started, ":")
		if len(parts) != 2 {
			return false
		}
		boot := parts[0]
		if len(boot) != 36 || boot[8] != '-' || boot[13] != '-' || boot[18] != '-' || boot[23] != '-' || strings.ToLower(boot) != boot {
			return false
		}
		bytes, err := hex.DecodeString(strings.ReplaceAll(boot, "-", ""))
		if err != nil || len(bytes) != 16 {
			return false
		}
		started = parts[1]
	default:
		return false
	}
	value, err := strconv.ParseUint(started, 10, 64)
	return err == nil && value > 0 && strconv.FormatUint(value, 10) == started
}

func (process ProcessIdentity) ConfirmedStopped() bool {
	if !process.Valid() || process.Platform != runtime.GOOS {
		return false
	}
	host, err := ProcessHost()
	if err != nil || host != process.Host {
		return false
	}
	started, stopped, err := inspectScopeProcess(process.PID)
	return err == nil && (stopped || (started != "" && started != process.Started))
}
