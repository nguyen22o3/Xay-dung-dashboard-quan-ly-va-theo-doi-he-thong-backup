package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"backup-monitor-backend/internal/backupops"
)

type categoryFlags []string

func (f *categoryFlags) String() string { return strings.Join(*f, ",") }
func (f *categoryFlags) Set(value string) error {
	for _, cat := range backupops.Categories {
		if cat == value {
			*f = append(*f, value)
			return nil
		}
	}
	return errors.New("Invalid category")
}
func run(args []string) error {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println(backupops.Version)
		return nil
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("Công cụ sản xuất chỉ chạy dưới root trên Linux")
	}
	if len(args) == 0 {
		return errors.New("Usage: backup-manager config|layout|install|version")
	}
	controller := backupops.NewController()
	switch args[0] {
	case "versions":
		if len(args) >= 3 && args[1] == "install" {
			if len(args) == 3 && args[2] == "--apply" {
				return controller.InstallDailyVersions(true, os.Stdout)
			}
			if len(args) == 3 && args[2] == "--preview" {
				return controller.InstallDailyVersions(false, os.Stdout)
			}
			return errors.New("Use versions install --preview|--apply")
		}
		if len(args) < 3 {
			return errors.New("Usage: backup-manager versions commit <category> <file> | drive <day> [--apply]")
		}
		unlock, err := backupops.VersionLock()
		if err != nil {
			return err
		}
		defer unlock()
		if args[1] == "commit" && len(args) == 4 {
			layout, err := controller.Layout()
			if err != nil {
				return err
			}
			return layout.CommitVersion(args[2], args[3], os.Stdout)
		}
		if args[1] == "drive" && (len(args) == 3 || len(args) == 4 && args[3] == "--apply") {
			return controller.ReconcileDriveVersions(args[2], len(args) == 4, os.Stdout)
		}
		return errors.New("Invalid version operation")
	case "config":
		// stdout is exactly one JSON object; errors never include command stderr
		// or source script contents. Successful SSH delivery includes error JSON.
		if len(args) != 1 {
			return errors.New("config reads one JSON request from stdin")
		}
		request, err := backupops.DecodeRequest(os.Stdin)
		var result interface{}
		if err == nil {
			result, err = controller.Dispatch(request)
		}
		if err != nil {
			result = map[string]string{"error": err.Error()}
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "layout":
		if len(args) < 2 {
			return errors.New("Usage: backup-manager layout organize|prune")
		}
		flags := flag.NewFlagSet("layout", flag.ContinueOnError)
		var categories categoryFlags
		flags.Var(&categories, "category", "site, database or panel; repeatable")
		apply := flags.Bool("apply", false, "move legacy archives after validation")
		delete := flags.Bool("delete", false, "delete expired archives")
		category := ""
		remaining := args[2:]
		if args[1] == "prune" {
			if len(remaining) == 0 {
				return errors.New("prune requires a category")
			}
			category = remaining[0]
			remaining = remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("Unexpected arguments")
		}
		if args[1] != "prune" && args[1] != "organize" {
			return errors.New("Invalid layout operation")
		}
		if args[1] == "organize" && *delete || args[1] == "prune" && (*apply || len(categories) > 0) {
			return errors.New("Invalid flags for this layout operation")
		}
		unlock, err := backupops.LayoutLock()
		if err != nil {
			return err
		}
		defer unlock()
		layout, err := controller.Layout()
		if err != nil {
			return err
		}
		if args[1] == "prune" {
			return layout.Prune(category, *delete, os.Stdout)
		}
		if len(categories) == 0 {
			categories = backupops.Categories
		}
		_, err = layout.Organize(categories, *apply, os.Stdout)
		return err
	case "install":
		flags := flag.NewFlagSet("install", flag.ContinueOnError)
		apply := flags.Bool("apply", false, "update managed scripts; no cron or data changes")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("Unexpected install arguments")
		}
		return controller.Install(*apply, os.Stdout)
	case "audit":
		if len(args) != 1 {
			return errors.New("audit has no mutation flags")
		}
		layout, err := controller.Layout()
		if err != nil {
			return err
		}
		result, err := layout.AuditArchives()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "verify-manifest":
		if len(args) != 2 {
			return errors.New("verify-manifest requires a manifest path")
		}
		layout, err := controller.Layout()
		if err != nil {
			return err
		}
		result, err := layout.VerifyManifest(args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	return errors.New("Unknown operation")
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
