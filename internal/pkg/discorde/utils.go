package discorde

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func getReleaseVersion() string {
	envs := []string{
		"SOURCE_VERSION",
	}
	for _, e := range envs {
		if release := os.Getenv(e); release != "" {
			return release
		}
	}

	cmd := exec.Command("git", "describe", "--long", "--always", "--dirty")
	b, err := cmd.Output()
	if err != nil {
		return "unknown"
	}

	release := strings.TrimSpace(string(b))
	return release
}

func baseFunctionName(name string) string {
	if i := strings.LastIndex(name, "."); i != -1 {
		return name[i+1:]
	}
	return name

}

func callerFunctionName() string {
	pcs := make([]uintptr, 1)
	runtime.Callers(3, pcs)
	callersFrames := runtime.CallersFrames(pcs)
	callerFrame, _ := callersFrames.Next()
	return baseFunctionName(callerFrame.Function)
}
