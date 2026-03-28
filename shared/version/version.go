package version

// Set at build time via -ldflags:
//
//	go build -ldflags "-X github.com/moebius-oss/moebius/shared/version.Version=1.0.0
//	  -X github.com/moebius-oss/moebius/shared/version.GitCommit=$(git rev-parse --short HEAD)
//	  -X github.com/moebius-oss/moebius/shared/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func FullVersion() string {
	return Version + " (" + GitCommit + ") built " + BuildTime
}
