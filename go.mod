module code-agent

go 1.24.0

require (
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.36.11
	modernc.org/sqlite v1.38.2
)

replace (
	golang.org/x/sys => golang.org/x/sys v0.35.0
	modernc.org/libc => modernc.org/libc v1.66.3
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/net v0.22.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240318140521-94a12d6c2237 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
