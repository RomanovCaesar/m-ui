package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Importing Xray / 3x-ui configurations. Everything is added to what the panel
// already has: nothing is deleted, an inbound whose port is taken is skipped (the
// existing one stays), and names that are taken get a suffix.

const maximumXrayImportInput = 64 << 20

type xrayImportResult struct {
	Source           string   `json:"source"`
	Inbounds         int      `json:"inbounds"`
	InboundsSkipped  int      `json:"inboundsSkipped"`
	Clients          int      `json:"clients"`
	Outbounds        int      `json:"outbounds"`
	Groups           int      `json:"groups"`
	OutboundsSkipped int      `json:"outboundsSkipped"`
	Rules            int      `json:"rules"`
	RulesSkipped     int      `json:"rulesSkipped"`
	Warnings         int      `json:"warnings"`
	Highlights       []string `json:"highlights"`
	Log              string   `json:"log"`
}

func (a *App) handleXrayImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumXrayImportInput+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("导入文件过大或无法读取")})
		return
	}
	defer r.MultipartForm.RemoveAll()
	var data []byte
	if file, _, err := r.FormFile("file"); err == nil {
		data, err = io.ReadAll(io.LimitReader(file, maximumXrayImportInput+1))
		file.Close()
		if err != nil || len(data) > maximumXrayImportInput {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("导入文件过大或无法读取")})
			return
		}
	} else {
		data = []byte(r.FormValue("content"))
	}
	source, err := parseXrayImportSource(data)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	result, err := a.manager.importXray(source)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error()), Data: result})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: result})
}

// xrayImportName makes a tag or remark usable as a listener or outbound name: it
// must not contain what separates Mihomo rule fields (",", "/", "(", ")").
func xrayImportName(value string) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case ',', '/', '(', ')', '\n', '\r', '\t':
			return '-'
		}
		return r
	}, strings.TrimSpace(value))
	if runes := []rune(value); len(runes) > 64 {
		value = string(runes[:64])
	}
	return strings.TrimSpace(value)
}

func uniqueImportName(base string, taken map[string]bool) string {
	if base == "" {
		base = "xray"
	}
	name := base
	for index := 1; taken[name]; index++ {
		if index == 1 {
			name = base + "-xray"
		} else {
			name = fmt.Sprintf("%s-xray-%d", base, index)
		}
	}
	taken[name] = true
	return name
}

func isXUIInternalInbound(in xrayImportInbound) bool {
	switch in.Tag {
	case "api", "panel-egress", "metrics":
		return true
	}
	loopback := in.Listen == "127.0.0.1" || in.Listen == "::1" || in.Listen == "localhost"
	return loopback && (in.Protocol == "tunnel" || in.Protocol == "dokodemo-door" || (in.Protocol == "socks" && len(mapList(in.Settings["accounts"])) == 0))
}

// reserveSubscriptionToken keeps a 3x-ui subId as the user's subscription token
// when it has m-ui's token format and no other user holds it, so existing
// subscription links keep working once the subscription path matches.
func (m *CoreManager) reserveSubscriptionToken(username, token string) bool {
	if !validSubscriptionToken(token) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.SubscriptionTokens == nil {
		m.state.SubscriptionTokens = map[string]string{}
	}
	if current := m.state.SubscriptionTokens[username]; validSubscriptionToken(current) {
		return current == token
	}
	for other, existing := range m.state.SubscriptionTokens {
		if other != username && existing == token {
			return false
		}
	}
	m.state.SubscriptionTokens[username] = token
	return true
}

func (m *CoreManager) importXray(input xrayImportSource) (xrayImportResult, error) {
	result := xrayImportResult{Source: input.Kind}
	log := &xrayImportLog{}
	log.lines = append(log.lines, "m-ui import from "+input.Kind+" — "+time.Now().Format("2006-01-02 15:04:05"))
	for _, note := range input.Notes {
		log.info("%s", note)
	}

	m.mu.Lock()
	settings := m.state.Settings
	existingInbounds := append([]Inbound(nil), m.state.Inbounds...)
	existingOutbounds := append([]MihomoOutbound(nil), m.state.Outbounds...)
	existingRules := append([]MihomoRoutingRule(nil), m.state.RoutingRules...)
	m.mu.Unlock()

	// ---- inbounds ----
	log.section("Inbounds")
	inboundNames := map[string]bool{}
	for _, inbound := range existingInbounds {
		inboundNames[inbound.Name] = true
	}
	importedInbound := map[string]string{} // xray tag -> listener name
	usernamesByEmail := map[string][]string{}
	occupied := append([]Inbound(nil), existingInbounds...)
	limitIPNoted := false
	for _, source := range input.Inbounds {
		label := valueOr(source.Tag, fmt.Sprintf("%s:%v", source.Protocol, source.Port))
		if isXUIInternalInbound(source) {
			log.info("%s: skipped (3x-ui internal inbound)", label)
			continue
		}
		if source.NodeID {
			log.warn("%s: skipped — it runs on a remote 3x-ui node, not on this server", label)
			result.InboundsSkipped++
			continue
		}
		if source.FallbackOf != "" {
			log.warn("%s: it was a fallback target of %s; Mihomo has no fallbacks, so it is imported as a separate listener on its own port", label, source.FallbackOf)
		}
		listener, skip := xrayListener(source, log)
		if skip != "" {
			log.warn("%s: skipped — %s", label, skip)
			result.InboundsSkipped++
			continue
		}
		port := intValue(listener["port"])
		listen := outboundStringValue(listener["listen"])
		conflict := ""
		for _, existing := range occupied {
			if existing.Port == port && listenAddressesOverlap(existing.Listen, listen) {
				conflict = fmt.Sprintf("port %d is already used by inbound %q (kept)", port, existing.Name)
				break
			}
		}
		if conflict == "" {
			if reserved := reservedPanelPortConflict(settings, listen, port); reserved != "" {
				conflict = fmt.Sprintf("port %d is used by the panel (%s)", port, reserved)
			}
		}
		if conflict != "" {
			log.warn("%s: skipped — %s", label, conflict)
			result.InboundsSkipped++
			continue
		}
		base := xrayImportName(source.Remark)
		if base == "" {
			base = xrayImportName(source.Tag)
		}
		if base == "" {
			base = fmt.Sprintf("%s-%d", source.Protocol, port)
		}
		name := uniqueImportName(base, inboundNames)
		listener["name"] = name

		// Users. A 3x-ui subId groups one person's clients across inbounds, and so
		// does an m-ui username: use it when the database provides one.
		byUsername := map[string]xrayImportClient{}
		usedUsernames := map[string]bool{}
		flowAllowed := xrayFlowAllowed(source)
		var users []any
		userMap := map[string]any{}
		for _, client := range source.Clients {
			username := xrayImportName(client.Email)
			if source.FromDB && validSubscriptionToken(client.SubID) && !usedUsernames[client.SubID] {
				username = client.SubID
			}
			if username == "" {
				username = fmt.Sprintf("user-%d", len(byUsername)+1)
			}
			for index := 2; usedUsernames[username]; index++ {
				username = fmt.Sprintf("%s-%d", xrayImportName(client.Email), index)
			}
			switch source.Protocol {
			case "vless":
				if client.ID == "" {
					log.warn("%s: client %s has no UUID and was skipped", name, client.Email)
					continue
				}
				entry := map[string]any{"username": username, "uuid": client.ID}
				if client.Flow != "" && flowAllowed {
					entry["flow"] = client.Flow
				}
				users = append(users, entry)
			case "vmess":
				if client.ID == "" {
					log.warn("%s: client %s has no UUID and was skipped", name, client.Email)
					continue
				}
				users = append(users, map[string]any{"username": username, "uuid": client.ID, "alterId": 0})
			case "trojan":
				if client.Password == "" {
					log.warn("%s: client %s has no password and was skipped", name, client.Email)
					continue
				}
				users = append(users, map[string]any{"username": username, "password": client.Password})
			case "hysteria", "hysteria2":
				secret := valueOr(client.Auth, client.Password)
				if secret == "" {
					log.warn("%s: client %s has no auth and was skipped", name, client.Email)
					continue
				}
				userMap[username] = secret
			case "tuic":
				id := client.ID
				if id == "" || client.Password == "" {
					log.warn("%s: client %s has no UUID or password and was skipped", name, client.Email)
					continue
				}
				username = id
				userMap[id] = client.Password
			}
			usedUsernames[username] = true
			byUsername[username] = client
		}
		switch source.Protocol {
		case "vless", "vmess", "trojan":
			listener["users"] = users
		case "hysteria", "hysteria2", "tuic":
			listener["users"] = userMap
		}
		if needsClients := containsString([]string{"vless", "vmess", "trojan", "hysteria", "hysteria2", "tuic"}, source.Protocol); needsClients && len(byUsername) == 0 {
			log.warn("%s: skipped — no client could be imported", label)
			result.InboundsSkipped++
			delete(inboundNames, name)
			continue
		}

		inbound, err := inboundFromYAMLMap(listener)
		if err != nil {
			log.warn("%s: skipped — %v", label, err)
			result.InboundsSkipped++
			delete(inboundNames, name)
			continue
		}
		inbound.Enabled = source.Enabled
		inbound.Total = source.Total
		inbound.ExpiryTime = source.ExpiryTime
		inbound.Traffic.Up, inbound.Traffic.Down = source.Up, source.Down
		if containsString([]string{"never", "hourly", "daily", "weekly", "monthly"}, source.TrafficReset) {
			inbound.TrafficReset = source.TrafficReset
			if source.TrafficReset == "monthly" && source.ResetDay >= 1 && source.ResetDay <= 31 {
				inbound.TrafficResetDay = source.ResetDay
			}
		}
		inbound.Notes = "Imported from " + input.Kind
		if source.Tag != "" {
			inbound.Notes += " (" + source.Tag + ")"
		}
		for index := range inbound.Clients {
			client := &inbound.Clients[index]
			from, ok := byUsername[client.Username]
			if !ok && source.Protocol == "shadowsocks" && len(source.Clients) == 1 {
				from, ok = source.Clients[0], true
			}
			if !ok {
				continue
			}
			if from.Email != "" {
				client.Name = from.Email
			}
			client.Enabled = from.Enabled
			client.Total = from.TotalBytes
			switch {
			case from.ExpiryTime > 0:
				client.ExpiryTime = from.ExpiryTime
			case from.ExpiryTime < 0:
				client.ExpireAfterDays = int((-from.ExpiryTime + 86400000 - 1) / 86400000)
			}
			client.Traffic.Up, client.Traffic.Down = from.Up, from.Down
			client.Traffic.AllTimeUp, client.Traffic.AllTimeDown = from.Up, from.Down
			if from.AllTime > from.Up+from.Down {
				client.Traffic.AllTimeDown = from.AllTime - from.Up
			}
			client.Notes = from.Comment
			if from.LimitIP > 0 && !limitIPNoted {
				limitIPNoted = true
				log.warn("per-client IP limits (limitIp) are not supported by m-ui and were dropped")
			}
			if xrayImportSchedule(client, from) {
				log.info("%s: %s keeps its traffic reset / renewal schedule", name, from.Email)
			}
			if source.FromDB && client.Username == from.SubID {
				if m.reserveSubscriptionToken(client.Username, from.SubID) {
					log.info("%s: %s keeps its subscription id %s as m-ui subscription token", name, from.Email, from.SubID)
				}
			}
		}
		if err := m.saveInbound(inbound); err != nil {
			log.warn("%s: skipped — %s", label, err.Error())
			result.InboundsSkipped++
			delete(inboundNames, name)
			continue
		}
		occupied = append(occupied, inbound)
		importedInbound[source.Tag] = name
		for username, client := range byUsername {
			usernamesByEmail[client.Email] = append(usernamesByEmail[client.Email], username)
		}
		result.Inbounds++
		result.Clients += len(inbound.Clients)
		renamed := ""
		if name != base {
			renamed = fmt.Sprintf(" (renamed from %q: the name was taken)", base)
		}
		log.info("%s → %s %s on port %d, %d client(s)%s", label, inbound.Type, name, port, len(inbound.Clients), renamed)
	}

	// ---- outbounds ----
	log.section("Outbounds")
	outboundNames := map[string]bool{}
	for _, builtin := range mihomoBuiltins {
		outboundNames[builtin] = true
	}
	for _, outbound := range existingOutbounds {
		outboundNames[outbound.Name] = true
	}
	target := map[string]string{} // xray outbound tag -> Mihomo target
	type pendingProxy struct {
		tag, dialer string
		proxy       map[string]any
	}
	var pending []pendingProxy
	var sourceTags []string
	for index, raw := range input.Outbounds {
		tag := outboundStringValue(raw["tag"])
		label := valueOr(tag, fmt.Sprintf("outbound #%d", index+1))
		proxy, builtin, dialer, skip := xrayOutbound(raw, log)
		switch {
		case skip != "":
			log.warn("%s: skipped — %s", label, skip)
			result.OutboundsSkipped++
			continue
		case builtin != "":
			target[tag] = builtin
			log.info("%s → %s", label, builtin)
			continue
		}
		base := xrayImportName(tag)
		if base == "" {
			base = fmt.Sprintf("xray-%d", index+1)
		}
		name := uniqueImportName(base, outboundNames)
		proxy["name"] = name
		target[tag] = name
		sourceTags = append(sourceTags, tag)
		pending = append(pending, pendingProxy{tag: tag, dialer: dialer, proxy: proxy})
	}
	if len(input.Outbounds) > 0 {
		first := input.Outbounds[0]
		if protocol := strings.ToLower(outboundStringValue(first["protocol"])); protocol != "freedom" && protocol != "direct" {
			log.warn("Xray sent traffic no rule matched to its first outbound %q; m-ui keeps its own final MATCH rule — change its target in Mihomo Settings → Routing if needed", outboundStringValue(first["tag"]))
		}
	}
	accepted := append([]MihomoOutbound(nil), existingOutbounds...)
	for _, item := range pending {
		label := valueOr(item.tag, outboundStringValue(item.proxy["name"]))
		if item.dialer != "" {
			if name, ok := target[item.dialer]; ok && name != "DIRECT" && name != "REJECT" {
				item.proxy["dialer-proxy"] = name
			} else if !ok {
				log.warn("%s: its dialer proxy %q was not imported and was dropped", label, item.dialer)
			}
		}
		outbound, err := mihomoOutboundFromYAMLMap(item.proxy, false)
		if err == nil {
			_, err = normalizeMihomoOutbounds(append(append([]MihomoOutbound(nil), accepted...), outbound))
		}
		if err != nil {
			log.warn("%s: skipped — %s", label, err.Error())
			result.OutboundsSkipped++
			delete(target, item.tag)
			continue
		}
		accepted = append(accepted, outbound)
		result.Outbounds++
		renamed := ""
		if outbound.Name != xrayImportName(item.tag) {
			renamed = " (renamed: the name was taken)"
		}
		log.info("%s → %s proxy %s%s", label, outbound.Type, outbound.Name, renamed)
	}
	balancerTarget := map[string]string{}
	for _, balancer := range input.Balancers {
		tag := outboundStringValue(balancer["tag"])
		group, skip := xrayBalancerGroup(balancer, sourceTags, func(tag string) (string, bool) {
			name, ok := target[tag]
			return name, ok && name != "DIRECT" && name != "REJECT"
		})
		if skip != "" {
			log.warn("balancer %s: skipped — %s", tag, skip)
			result.OutboundsSkipped++
			continue
		}
		group["name"] = uniqueImportName(xrayImportName(valueOr(tag, "balancer")), outboundNames)
		outbound, err := mihomoOutboundFromYAMLMap(group, true)
		if err == nil {
			_, err = normalizeMihomoOutbounds(append(append([]MihomoOutbound(nil), accepted...), outbound))
		}
		if err != nil {
			log.warn("balancer %s: skipped — %s", tag, err.Error())
			result.OutboundsSkipped++
			continue
		}
		accepted = append(accepted, outbound)
		balancerTarget[tag] = outbound.Name
		result.Groups++
		log.info("balancer %s → %s group %s", tag, outbound.Type, outbound.Name)
	}

	// ---- routing rules ----
	log.section("Routing rules")
	normalized, err := normalizeMihomoOutbounds(accepted)
	if err != nil {
		return result, fmt.Errorf("导入的出站无法保存: %v", err)
	}
	ctx := xrayRuleContext{
		inbound: func(tag string) (string, bool) { name, ok := importedInbound[tag]; return name, ok },
		target: func(tag string) (string, bool) {
			name, ok := target[tag]
			return name, ok
		},
		balancer: func(tag string) (string, bool) { name, ok := balancerTarget[tag]; return name, ok },
		usernames: func(email string) []string {
			return usernamesByEmail[email]
		},
		noResolve: input.DomainStrategy == "" || strings.EqualFold(input.DomainStrategy, "AsIs"),
	}
	existingKey := map[string]bool{}
	for _, rule := range existingRules {
		existingKey[fmt.Sprintf("%s|%s|%s|%v", rule.Type, rule.Payload, rule.Target, rule.NoResolve)] = true
	}
	var newRules []MihomoRoutingRule
	for index, raw := range input.Rules {
		label := fmt.Sprintf("rule #%d", index+1)
		if tags := stringListRaw(raw["inboundTag"]); outboundStringValue(raw["outboundTag"]) == "api" || containsString(tags, "api") || containsString(tags, "panel-egress") {
			log.info("%s: skipped (3x-ui internal rule)", label)
			continue
		}
		rules, why := xrayRule(raw, ctx)
		if len(rules) == 0 {
			log.warn("%s: skipped — %s", label, why)
			result.RulesSkipped++
			continue
		}
		if why != "" {
			log.warn("%s: %s", label, why)
		}
		for _, rule := range rules {
			key := fmt.Sprintf("%s|%s|%s|%v", rule.Type, rule.Payload, rule.Target, rule.NoResolve)
			if existingKey[key] {
				log.info("%s: %s,%s → %s already exists", label, rule.Type, rule.Payload, rule.Target)
				continue
			}
			if _, err := normalizeMihomoRoutingRules([]MihomoRoutingRule{rule}, normalized); err != nil {
				log.warn("%s: %s,%s skipped — %s", label, rule.Type, rule.Payload, err.Error())
				result.RulesSkipped++
				continue
			}
			existingKey[key] = true
			newRules = append(newRules, rule)
			log.info("%s → %s,%s,%s", label, rule.Type, rule.Payload, rule.Target)
		}
	}
	result.Rules = len(newRules)

	if result.Outbounds+result.Groups > 0 || len(newRules) > 0 {
		combined := make([]MihomoRoutingRule, 0, len(existingRules)+len(newRules))
		inserted := false
		for _, rule := range existingRules {
			if strings.EqualFold(rule.Type, "MATCH") && !inserted {
				combined = append(combined, newRules...)
				inserted = true
			}
			combined = append(combined, rule)
		}
		if !inserted {
			combined = append(combined, newRules...)
		}
		if err := m.updateMihomoSettings(mihomoSettingsPayload{Outbounds: accepted, RoutingRules: combined}); err != nil {
			log.warn("outbounds and routing rules could not be saved: %v", err)
			result.Outbounds, result.Groups, result.Rules = 0, 0, 0
		}
	}

	if err := m.writeConfig(); err != nil {
		log.warn("the Mihomo configuration could not be written: %v", err)
	}

	log.section("Summary")
	log.info("inbounds: %d imported, %d skipped; clients: %d", result.Inbounds, result.InboundsSkipped, result.Clients)
	log.info("outbounds: %d proxies and %d groups imported, %d skipped", result.Outbounds, result.Groups, result.OutboundsSkipped)
	log.info("routing rules: %d imported, %d skipped", result.Rules, result.RulesSkipped)
	log.info("restart Mihomo to apply the imported configuration")
	result.Warnings = log.warnings
	for _, line := range log.lines {
		if strings.HasPrefix(line, "  ! ") && len(result.Highlights) < 8 {
			result.Highlights = append(result.Highlights, strings.TrimPrefix(line, "  ! "))
		}
	}
	result.Log = strings.Join(log.lines, "\n") + "\n"
	if result.Inbounds == 0 && result.Outbounds == 0 && result.Groups == 0 && result.Rules == 0 {
		return result, errors.New("没有可以导入的内容，详情见转换日志")
	}
	return result, nil
}

// xrayImportSchedule copies a 3x-ui client's periodic traffic reset and expiry
// renewal. 3x-ui prefers the renewal day of month, then the weekday, then the
// number of days, so only that one is kept.
func xrayImportSchedule(client *Client, from xrayImportClient) bool {
	if containsString([]string{"hourly", "daily", "weekly", "monthly"}, from.TrafficReset) {
		client.TrafficReset = from.TrafficReset
		if from.TrafficReset == "monthly" && from.TrafficResetDay >= 1 && from.TrafficResetDay <= 31 {
			client.TrafficResetDay = from.TrafficResetDay
		}
	}
	switch {
	case from.RenewDay >= 1 && from.RenewDay <= 31:
		client.RenewDay = from.RenewDay
	case from.RenewWeekday >= 1 && from.RenewWeekday <= 7:
		client.RenewWeekday = from.RenewWeekday
	case from.Renew >= 1 && from.Renew <= 3650:
		client.RenewDays = from.Renew
	}
	if client.RenewDays > 0 || client.RenewDay > 0 || client.RenewWeekday > 0 {
		client.RenewMax = max(from.RenewMax, 0)
		client.RenewCount = max(from.RenewCount, 0)
	}
	return client.TrafficReset != "" || client.RenewDays > 0 || client.RenewDay > 0 || client.RenewWeekday > 0
}
