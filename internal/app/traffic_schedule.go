package app

import (
	"errors"
	"fmt"
	"time"
)

// Periodic traffic resets (inbound-wide and per client) and automatic client
// renewal. Both follow 3x-ui 3.7+: a monthly reset day past the end of a short
// month falls on its last day, and renewal moves an expired client's expiry to
// the next period and clears its traffic.

var trafficResetPeriods = map[string]bool{"": true, "never": true, "hourly": true, "daily": true, "weekly": true, "monthly": true}

func validateResetDay(period string, day int) error {
	if !trafficResetPeriods[period] {
		return errors.New("流量重置周期无效")
	}
	if day < 0 || day > 31 {
		return errors.New("每月重置日必须在 1-31 之间")
	}
	return nil
}

func validateClientSchedule(client Client) error {
	if err := validateResetDay(client.TrafficReset, client.TrafficResetDay); err != nil {
		return err
	}
	modes := 0
	for _, value := range []int{client.RenewDays, client.RenewDay, client.RenewWeekday} {
		if value < 0 {
			return errors.New("自动续期设置无效")
		}
		if value > 0 {
			modes++
		}
	}
	switch {
	case modes > 1:
		return errors.New("自动续期只能选择一种方式")
	case client.RenewDays > 3650:
		return errors.New("自动续期天数必须在 1-3650 之间")
	case client.RenewDay > 31:
		return errors.New("每月续期日必须在 1-31 之间")
	case client.RenewWeekday > 7:
		return errors.New("每周续期日必须在 1-7 之间")
	case client.RenewMax < 0 || client.RenewCount < 0:
		return errors.New("自动续期设置无效")
	}
	return nil
}

// carryClientSchedule keeps the counters the panel owns when a client is saved
// from a form, and restarts the reset period when its schedule changed, so a
// schedule set today does not clear traffic at once because the client is old.
func carryClientSchedule(stored Client, incoming *Client, now time.Time) {
	incoming.RenewCount = stored.RenewCount
	if stored.TrafficReset == incoming.TrafficReset && stored.TrafficResetDay == incoming.TrafficResetDay {
		incoming.LastTrafficReset = stored.LastTrafficReset
	} else {
		incoming.LastTrafficReset = now.Format(time.RFC3339)
	}
}

func carryInboundSchedule(stored Inbound, incoming *Inbound, now time.Time) {
	if stored.TrafficReset == incoming.TrafficReset && stored.TrafficResetDay == incoming.TrafficResetDay {
		incoming.LastTrafficReset = stored.LastTrafficReset
	} else {
		incoming.LastTrafficReset = now.Format(time.RFC3339)
	}
}

// monthDay is the given day of a month, moved to its last day when the month is
// shorter, at midnight.
func monthDay(year int, month time.Month, day int, location *time.Location) time.Time {
	if day < 1 {
		day = 1
	}
	if last := time.Date(year, month+1, 0, 0, 0, 0, 0, location).Day(); day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, location)
}

// trafficResetDue reports whether a reset boundary of the period passed after
// the last reset. Times must be in the panel time zone.
func trafficResetDue(period string, day int, last, now time.Time) bool {
	if !last.Before(now) {
		return false
	}
	switch period {
	case "hourly":
		return now.Sub(last) >= time.Hour
	case "daily":
		return last.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
	case "weekly":
		nowYear, nowWeek := now.ISOWeek()
		lastYear, lastWeek := last.ISOWeek()
		return nowYear != lastYear || nowWeek != lastWeek
	case "monthly":
		boundary := monthDay(now.Year(), now.Month(), day, now.Location())
		if now.Before(boundary) {
			boundary = monthDay(now.Year(), now.Month()-1, day, now.Location())
		}
		return last.Before(boundary)
	default:
		return false
	}
}

func scheduleStart(lastReset, createdAt string, now time.Time) time.Time {
	for _, value := range []string{lastReset, createdAt} {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.In(now.Location())
		}
	}
	return now
}

// nextRenewal is the expiry one renewal period after expiry.
func nextRenewal(client Client, expiry time.Time) time.Time {
	switch {
	case client.RenewDay > 0:
		candidate := monthDay(expiry.Year(), expiry.Month(), client.RenewDay, expiry.Location())
		if !candidate.After(expiry) {
			candidate = monthDay(expiry.Year(), expiry.Month()+1, client.RenewDay, expiry.Location())
		}
		return candidate
	case client.RenewWeekday > 0:
		// time.Weekday counts from Sunday = 0; RenewWeekday from Monday = 1.
		days := (client.RenewWeekday%7 - int(expiry.Weekday()) + 7) % 7
		if days == 0 {
			days = 7
		}
		return time.Date(expiry.Year(), expiry.Month(), expiry.Day()+days, 0, 0, 0, 0, expiry.Location())
	default:
		return expiry.AddDate(0, 0, client.RenewDays)
	}
}

// renewClient extends an expired client with automatic renewal, catching up on
// periods missed while the panel was down, and reports whether it changed.
func renewClient(client *Client, now time.Time) bool {
	if client.RenewDays <= 0 && client.RenewDay <= 0 && client.RenewWeekday <= 0 {
		return false
	}
	if client.ExpiryTime <= 0 || client.ExpiryTime > now.UnixMilli() {
		return false
	}
	expiry := time.UnixMilli(client.ExpiryTime).In(now.Location())
	renewals := 0
	for !expiry.After(now) {
		if client.RenewMax > 0 && client.RenewCount+renewals >= client.RenewMax {
			break
		}
		next := nextRenewal(*client, expiry)
		if !next.After(expiry) {
			return false
		}
		expiry = next
		renewals++
	}
	if renewals == 0 {
		return false
	}
	client.ExpiryTime = expiry.UnixMilli()
	client.RenewCount += renewals
	if expiry.After(now) {
		client.Traffic.Up, client.Traffic.Down = 0, 0
	}
	return true
}

// servedKeys lists the inbounds and clients the generated configuration lets in,
// so a reset or renewal that brings one back can restart Mihomo.
func servedKeys(state State, now int64) map[string]bool {
	keys := map[string]bool{}
	for _, inbound := range state.Inbounds {
		if inboundDepleted(inbound, now) {
			continue
		}
		keys[inbound.ID] = true
		for _, client := range inbound.Clients {
			if !clientDepleted(client, now) {
				keys[inbound.ID+":"+client.ID] = true
			}
		}
	}
	return keys
}

func (m *CoreManager) applyScheduledTrafficResets() {
	m.mu.Lock()
	// 周期重置要按设置里的时区判断"跨天/跨周/跨月"，否则服务器跑 UTC 而用户在东八区时，
	// 每日重置会拖到当地早上八点才发生。
	location, locationErr := resolveTimeLocation(m.state.Settings.TimeLocation)
	if locationErr != nil {
		location = time.Local
	}
	now := time.Now().In(location)
	before := servedKeys(m.state, now.UnixMilli())
	changed := false
	for index := range m.state.Inbounds {
		inbound := &m.state.Inbounds[index]
		if inbound.TrafficReset != "" && inbound.TrafficReset != "never" &&
			trafficResetDue(inbound.TrafficReset, inbound.TrafficResetDay, scheduleStart(inbound.LastTrafficReset, inbound.CreatedAt, now), now) {
			inbound.Traffic.Up, inbound.Traffic.Down = 0, 0
			for clientIndex := range inbound.Clients {
				inbound.Clients[clientIndex].Traffic.Up, inbound.Clients[clientIndex].Traffic.Down = 0, 0
			}
			inbound.LastTrafficReset = now.Format(time.RFC3339)
			m.addLogLocked(fmt.Sprintf("Inbound %s 已执行 %s 周期流量重置", inbound.Name, inbound.TrafficReset))
			changed = true
		}
		for clientIndex := range inbound.Clients {
			client := &inbound.Clients[clientIndex]
			if renewClient(client, now) {
				m.addLogLocked(fmt.Sprintf("客户端 %s/%s 已自动续期至 %s（第 %d 次）", inbound.Name, client.Name, time.UnixMilli(client.ExpiryTime).In(location).Format("2006-01-02 15:04"), client.RenewCount))
				changed = true
			}
			if !client.Enabled || client.TrafficReset == "" || client.TrafficReset == "never" {
				continue
			}
			if trafficResetDue(client.TrafficReset, client.TrafficResetDay, scheduleStart(client.LastTrafficReset, client.CreatedAt, now), now) {
				client.Traffic.Up, client.Traffic.Down = 0, 0
				client.LastTrafficReset = now.Format(time.RFC3339)
				m.addLogLocked(fmt.Sprintf("客户端 %s/%s 已执行 %s 周期流量重置", inbound.Name, client.Name, client.TrafficReset))
				changed = true
			}
		}
	}
	if !changed {
		m.mu.Unlock()
		return
	}
	_ = m.saveLocked()
	restored := false
	for key := range servedKeys(m.state, now.UnixMilli()) {
		if !before[key] {
			restored = true
			delete(m.enforcedClients, key)
		}
	}
	running := m.runningLocked()
	m.mu.Unlock()
	// A client or inbound the configuration had shut out is allowed again: the
	// listeners only learn that from a new configuration.
	if restored && running {
		go func() {
			if err := m.restartCore(); err != nil {
				m.mu.Lock()
				m.addLogLocked("应用流量重置失败: " + err.Error())
				m.mu.Unlock()
			}
		}()
	}
}
