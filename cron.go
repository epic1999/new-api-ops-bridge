// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"context"
	"fmt"
	"io"
	"new-api-ops-bridge/common"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type cronJob struct {
	ID         string `json:"id"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	LastRun    string `json:"last_run,omitempty"`
	LastTaskID string `json:"last_task_id,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}
type cronSchedule struct {
	fields   [5]map[int]bool
	wildcard [5]bool
}

func parseCron(raw string) (cronSchedule, error) {
	var schedule cronSchedule
	fields := strings.Fields(raw)
	if len(fields) != 5 || len(raw) > 150 || strings.ContainsAny(raw, "\r\n") {
		return schedule, fmt.Errorf("use five numeric cron fields")
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		schedule.fields[i] = make(map[int]bool)
		schedule.wildcard[i] = strings.HasPrefix(field, "*")
		for _, item := range strings.Split(field, ",") {
			step := 1
			pieces := strings.Split(item, "/")
			if len(pieces) > 2 {
				return schedule, fmt.Errorf("invalid cron step")
			}
			if len(pieces) == 2 {
				var err error
				step, err = strconv.Atoi(pieces[1])
				if err != nil || step < 1 || step > bounds[i][1]+1 {
					return schedule, fmt.Errorf("invalid cron step")
				}
			}
			start, end := bounds[i][0], bounds[i][1]
			if pieces[0] != "*" {
				rangeParts := strings.Split(pieces[0], "-")
				if len(rangeParts) > 2 {
					return schedule, fmt.Errorf("invalid cron range")
				}
				var err error
				start, err = strconv.Atoi(rangeParts[0])
				if err != nil {
					return schedule, fmt.Errorf("invalid cron number")
				}
				end = start
				if len(rangeParts) == 2 {
					end, err = strconv.Atoi(rangeParts[1])
					if err != nil {
						return schedule, fmt.Errorf("invalid cron range")
					}
				} else if len(pieces) == 2 {
					end = bounds[i][1]
				}
			}
			if start < bounds[i][0] || end > bounds[i][1] || start > end {
				return schedule, fmt.Errorf("cron value out of range")
			}
			for n := start; n <= end; n += step {
				value := n
				if i == 4 && value == 7 {
					value = 0
				}
				schedule.fields[i][value] = true
			}
		}
	}
	return schedule, nil
}
func (s cronSchedule) matches(t time.Time) bool {
	if !s.fields[0][t.Minute()] || !s.fields[1][t.Hour()] || !s.fields[3][int(t.Month())] {
		return false
	}
	day, weekday := s.fields[2][t.Day()], s.fields[4][int(t.Weekday())]
	if !s.wildcard[2] && !s.wildcard[4] {
		return day || weekday
	}
	return day && weekday
}
func (b *bridge) cronPath() string { return filepath.Join(b.cfg.Home, ".new-api-ops-cron.json") }
func (b *bridge) loadCron() error {
	f, err := secureOpen(b.cronPath(), false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("cron storage must be an owner-only regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return fmt.Errorf("cron storage exceeds limit")
	}
	var jobs []cronJob
	if err = common.DecodeBytes(data, &jobs); err != nil || len(jobs) > 100 {
		return fmt.Errorf("invalid cron storage")
	}
	seen := map[string]bool{}
	for _, j := range jobs {
		if len(j.ID) != 43 || seen[j.ID] || validateCommand(j.Command) != nil {
			return fmt.Errorf("invalid cron job")
		}
		if _, err = parseCron(j.Schedule); err != nil {
			return err
		}
		seen[j.ID] = true
	}
	b.cronJobs = jobs
	return nil
}
func (b *bridge) saveCron(jobs []cronJob) error {
	data, err := common.Marshal(jobs)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(b.cfg.Home, ".ops-cron-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), b.cronPath())
}
func (b *bridge) cronList() any {
	b.cronMu.Lock()
	defer b.cronMu.Unlock()
	jobs := append([]cronJob{}, b.cronJobs...)
	return map[string]any{"jobs": jobs, "scheduler": "bridge-managed", "timezone": time.Now().Location().String(), "note": "Jobs survive bridge restarts; run while the bridge is active, max 100 jobs and 4 concurrent background tasks. Does not edit the OS crontab."}
}
func (b *bridge) cronCreate(a args) (any, error) {
	if _, err := parseCron(a.Schedule); err != nil {
		return nil, err
	}
	if err := validateCommand(a.Command); err != nil {
		return nil, err
	}
	if strings.ContainsAny(a.Command, "\r\n") {
		return nil, fmt.Errorf("cron command must be one line")
	}
	b.cronMu.Lock()
	defer b.cronMu.Unlock()
	if len(b.cronJobs) >= 100 {
		return nil, fmt.Errorf("maximum 100 cron jobs")
	}
	id, err := randomPassword()
	if err != nil {
		return nil, err
	}
	j := cronJob{ID: id, Schedule: strings.Join(strings.Fields(a.Schedule), " "), Command: a.Command}
	next := append(append([]cronJob{}, b.cronJobs...), j)
	if err = b.saveCron(next); err != nil {
		return nil, err
	}
	b.cronJobs = next
	return map[string]any{"ok": true, "job": j}, nil
}
func (b *bridge) cronDelete(id string) (any, error) {
	b.cronMu.Lock()
	defer b.cronMu.Unlock()
	next := []cronJob{}
	found := false
	for _, j := range b.cronJobs {
		if j.ID == id {
			found = true
		} else {
			next = append(next, j)
		}
	}
	if !found {
		return nil, fmt.Errorf("cron job not found")
	}
	if err := b.saveCron(next); err != nil {
		return nil, err
	}
	b.cronJobs = next
	return map[string]bool{"deleted": true}, nil
}
func (b *bridge) runCron(ctx context.Context) {
	for {
		now := time.Now()
		timer := time.NewTimer(time.Until(now.Truncate(time.Minute).Add(time.Minute)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case tick := <-timer.C:
			b.cronMu.Lock()
			jobs := append([]cronJob{}, b.cronJobs...)
			b.cronMu.Unlock()
			b.dispatchCron(jobs, tick)
		}
	}
}

func (b *bridge) dispatchCron(jobs []cronJob, tick time.Time) {
	for _, j := range jobs {
		schedule, err := parseCron(j.Schedule)
		if err != nil || !schedule.matches(tick) {
			continue
		}
		// Recheck under lock so a deleted job cannot start after deletion returns.
		b.cronMu.Lock()
		for i, current := range b.cronJobs {
			if current.ID != j.ID {
				continue
			}
			result, startErr := b.startTask(args{Command: j.Command, TimeoutSeconds: 3600})
			b.cronJobs[i].LastRun = tick.Format(time.RFC3339)
			b.cronJobs[i].LastTaskID, b.cronJobs[i].LastError = "", ""
			if startErr != nil {
				b.cronJobs[i].LastError = startErr.Error()
			} else {
				b.cronJobs[i].LastTaskID = result.(map[string]any)["id"].(string)
			}
			break
		}
		b.cronMu.Unlock()
	}
}
