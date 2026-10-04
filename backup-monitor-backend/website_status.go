package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const websiteProbeTimeoutSeconds = 15
const websiteSlowThresholdSeconds = 2
const websiteDiscoveryCommand = `set -o pipefail
find /www/wwwroot -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | LC_ALL=C sort`

var websiteHostname = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)

type websiteProbeResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Code   string `json:"code"`
	Time   string `json:"time"`
	Reason string `json:"reason,omitempty"`
}

func parseWebsiteProbe(name, output string) websiteProbeResult {
	result := websiteProbeResult{Name: name, Status: "UNKNOWN", Code: "000", Time: "", Reason: "probe_failed"}
	fields := strings.Split(strings.TrimSpace(output), ",")
	if len(fields) != 3 || len(fields[0]) != 3 {
		return result
	}
	code, codeErr := strconv.Atoi(fields[0])
	seconds, timeErr := strconv.ParseFloat(fields[1], 64)
	exit, exitErr := strconv.Atoi(fields[2])
	if codeErr != nil || timeErr != nil || exitErr != nil || code < 0 || code > 599 || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return result
	}
	result.Code, result.Time = fields[0], fmt.Sprintf("%.0fms", seconds*1000)
	switch {
	case exit == 28:
		// A deadline is not proof of an outage, even if no headers arrived.
		result.Status, result.Reason = "TIMEOUT", "timeout"
	case exit == 6 || exit == 7:
		result.Status, result.Reason = "OFFLINE", "connection_failed"
	case exit != 0 || code == 0:
		return result
	case (code >= 200 && code < 400) || code == 401 || code == 403:
		result.Status, result.Reason = "ONLINE", ""
		if seconds >= websiteSlowThresholdSeconds {
			result.Status, result.Reason = "SLOW", "slow_response"
		}
	default:
		result.Status, result.Reason = "ERROR", "http_error"
	}
	return result
}

func websiteProbeCommand(name string) string {
	// Callers validate the hostname; never interpolate arbitrary folder names.
	return fmt.Sprintf(`result=$(curl --noproxy '*' -o /dev/null -s -w '%%{http_code},%%{time_total}' --resolve '%s:80:127.0.0.1' --connect-timeout 3 --max-time %d 'http://%s/' 2>/dev/null)
exit_code=$?
printf '%%s,%%s\n' "$result" "$exit_code"`, name, websiteProbeTimeoutSeconds, name)
}

func collectWebsiteStatuses(run func(string) (string, error)) ([]byte, error) {
	out, err := run(websiteDiscoveryCommand)
	if err != nil {
		return nil, err
	}
	names, seen := []string{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "default" || !websiteHostname.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == 10 {
			break
		}
	}
	results := make([]websiteProbeResult, len(names))
	// At most two active requests: keep latency bounded without flooding PHP.
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for i := range jobs {
				response, err := run(websiteProbeCommand(names[i]))
				if err != nil {
					response = ""
				}
				results[i] = parseWebsiteProbe(names[i], response)
			}
		})
	}
	for i := range names {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return json.Marshal(results)
}
