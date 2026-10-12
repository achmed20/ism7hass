package version

import (
	"time"
)

type Version struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Branch  string `json:"branch"`
	Built   string `json:"built"`
	Now     string `json:"now"`
}

func GetVersion() Version {
	return Version{
		Project: PROJECT,
		Version: VERSION,
		Branch:  BRANCH,
		Built:   BUILD_DATE,
		Now:     time.Now().Format(time.RFC1123Z),
	}
}
