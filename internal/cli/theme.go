package cli

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

type Theme struct {
	Accent  string
	Success string
	Pending string
	Danger  string
	Muted   string
	Strong  string
}

func defaultTheme() Theme {
	return Theme{Accent: ansiCyan, Success: ansiGreen, Pending: ansiYellow, Danger: ansiRed, Muted: ansiDim, Strong: ansiBold}
}
