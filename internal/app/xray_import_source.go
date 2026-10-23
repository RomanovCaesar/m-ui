package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Reading the source of an Xray import: a config.json written by Xray / 3x-ui
// (from 3x-ui v2.9.3 up to the 3.x line), a pasted 3x-ui xray template, a single
// inbound or a list of inbounds, or a 3x-ui database backup (x-ui.db).
//
// config.json is what 3x-ui generates from its database, and it strips clients down
// to email/id/password/flow/method/auth: traffic limits, expiry, remarks and
// subscription ids are only in x-ui.db, and disabled or depleted clients are not
// written to config.json at all. The database source fills those in.

type xrayImportSource struct {
	Kind           string // "config.json" or "x-ui.db"
	Inbounds       []xrayImportInbound
	Outbounds      []map[string]any
	Rules          []map[string]any
	Balancers      []map[string]any
	DomainStrategy string
	Notes          []string
}

type xrayImportInbound struct {
	Tag      string
	Protocol string
	Listen   string
	Port     any
	Settings map[string]any
	Stream   map[string]any

	// Only known from x-ui.db.
	FromDB       bool
	Remark       string
	Enabled      bool
	Total        uint64
	ExpiryTime   int64
	Up, Down     uint64
	TrafficReset string
	NodeID       bool // the inbound runs on a remote 3x-ui node
	DisableFlow  bool // 3.7+: 3x-ui drops the clients' flow for this inbound
	Fallbacks    int  // 3.1+: rows of inbound_fallbacks with this inbound as master
	FallbackOf   string

	Clients []xrayImportClient
}

type xrayImportClient struct {
	Email    string
	ID       string // uuid
	Password string
	Flow     string
	Auth     string // hysteria
	Method   string // shadowsocks per-user method
	Security string // vmess

	Enabled    bool
	TotalBytes uint64
	ExpiryTime int64 // negative: expire this many ms after first use
	SubID      string
	Comment    string
	LimitIP    int
	Reset      string // 3.7+: per-client traffic reset schedule
	Up, Down   uint64
	AllTime    uint64
}

func parseXrayImportSource(data []byte) (xrayImportSource, error) {
	if isSQLiteFile(data) {
		return parseXUIDatabase(data)
	}
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if len(trimmed) == 0 {
		return xrayImportSource{}, errors.New("导入内容为空")
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return xrayImportSource{}, fmt.Errorf("无法识别的导入内容，请上传 Xray 的 config.json 或 3x-ui 的 x-ui.db: %v", err)
	}
	decoded = normalizeJSONNumbers(decoded)
	source := xrayImportSource{Kind: "config.json"}
	switch typed := decoded.(type) {
	case []any:
		// A list of inbounds, as copied from 3x-ui's inbound JSON.
		for _, item := range typed {
			if inbound := mapValue(item); inbound != nil {
				source.Inbounds = append(source.Inbounds, xrayInboundFromConfig(inbound))
			}
		}
	case map[string]any:
		if _, single := typed["protocol"]; single && typed["inbounds"] == nil {
			source.Inbounds = append(source.Inbounds, xrayInboundFromConfig(typed))
			break
		}
		for _, item := range anyList(typed["inbounds"]) {
			if inbound := mapValue(item); inbound != nil {
				source.Inbounds = append(source.Inbounds, xrayInboundFromConfig(inbound))
			}
		}
		source.Outbounds = mapList(typed["outbounds"])
		if routing := mapValue(typed["routing"]); routing != nil {
			source.Rules = mapList(routing["rules"])
			source.Balancers = mapList(routing["balancers"])
			source.DomainStrategy = outboundStringValue(routing["domainStrategy"])
		}
	default:
		return xrayImportSource{}, errors.New("无法识别的导入内容，请上传 Xray 的 config.json 或 3x-ui 的 x-ui.db")
	}
	if len(source.Inbounds) == 0 && len(source.Outbounds) == 0 && len(source.Rules) == 0 {
		return xrayImportSource{}, errors.New("没有找到可导入的 inbounds、outbounds 或 routing 规则")
	}
	return source, nil
}

// normalizeJSONNumbers turns json.Number into int64 when integral, float64
// otherwise, so ports and byte counts above 2^53 survive.
func normalizeJSONNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeJSONNumbers(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = normalizeJSONNumbers(item)
		}
		return typed
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		float, _ := typed.Float64()
		return float
	default:
		return value
	}
}

func anyList(value any) []any {
	list, _ := value.([]any)
	return list
}

func mapList(value any) []map[string]any {
	var result []map[string]any
	for _, item := range anyList(value) {
		if mapping := mapValue(item); mapping != nil {
			result = append(result, mapping)
		}
	}
	return result
}

func jsonObject(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || text == "null" {
			return nil
		}
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if decoder.Decode(&decoded) != nil {
			return nil
		}
		return mapValue(normalizeJSONNumbers(decoded))
	case []byte:
		return jsonObject(string(typed))
	}
	return nil
}

func xrayInboundFromConfig(raw map[string]any) xrayImportInbound {
	inbound := xrayImportInbound{
		Tag:      outboundStringValue(raw["tag"]),
		Protocol: strings.ToLower(outboundStringValue(raw["protocol"])),
		Listen:   strings.TrimSpace(outboundStringValue(raw["listen"])),
		Port:     raw["port"],
		Settings: jsonObject(raw["settings"]),
		Stream:   jsonObject(raw["streamSettings"]),
		Enabled:  true,
	}
	// 3x-ui's own inbound JSON (copied from the panel) carries the database
	// fields too.
	if remark := outboundStringValue(raw["remark"]); remark != "" {
		inbound.Remark = remark
	}
	if enable, ok := raw["enable"].(bool); ok {
		inbound.Enabled = enable
	}
	inbound.Total = uint64Value(raw["total"])
	inbound.ExpiryTime = int64Value(raw["expiryTime"])
	inbound.Up, inbound.Down = uint64Value(raw["up"]), uint64Value(raw["down"])
	inbound.TrafficReset = outboundStringValue(raw["trafficReset"])
	inbound.Clients = xrayClientsFromSettings(inbound.Settings)
	return inbound
}

func uint64Value(value any) uint64 {
	switch typed := value.(type) {
	case int64:
		if typed > 0 {
			return uint64(typed)
		}
	case float64:
		if typed > 0 {
			return uint64(typed)
		}
	case uint64:
		return typed
	case int:
		if typed > 0 {
			return uint64(typed)
		}
	}
	return 0
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case uint64:
		return int64(typed)
	}
	return 0
}

func xrayClientsFromSettings(settings map[string]any) []xrayImportClient {
	var clients []xrayImportClient
	for _, raw := range mapList(settings["clients"]) {
		client := xrayImportClient{
			Email:      outboundStringValue(raw["email"]),
			ID:         firstString(raw, "id", "uuid"),
			Password:   outboundStringValue(raw["password"]),
			Flow:       outboundStringValue(raw["flow"]),
			Auth:       outboundStringValue(raw["auth"]),
			Method:     outboundStringValue(raw["method"]),
			Security:   outboundStringValue(raw["security"]),
			Enabled:    true,
			TotalBytes: uint64Value(raw["totalGB"]),
			ExpiryTime: int64Value(raw["expiryTime"]),
			SubID:      outboundStringValue(raw["subId"]),
			Comment:    outboundStringValue(raw["comment"]),
			LimitIP:    intValue(raw["limitIp"]),
		}
		if enable, ok := raw["enable"].(bool); ok {
			client.Enabled = enable
		}
		if client.Flow == "xtls-rprx-vision-udp443" {
			client.Flow = "xtls-rprx-vision"
		}
		clients = append(clients, client)
	}
	return clients
}

// ---- x-ui.db ----

func parseXUIDatabase(data []byte) (xrayImportSource, error) {
	db, err := openSQLite(data)
	if err != nil {
		return xrayImportSource{}, fmt.Errorf("无法读取 x-ui.db: %v", err)
	}
	if !db.hasTable("inbounds") {
		return xrayImportSource{}, errors.New("这个数据库里没有 inbounds 表，不是 3x-ui 的 x-ui.db")
	}
	source := xrayImportSource{Kind: "x-ui.db"}
	rows, err := db.rows("inbounds")
	if err != nil {
		return xrayImportSource{}, fmt.Errorf("无法读取 x-ui.db: %v", err)
	}

	traffic := map[string]map[string]any{}
	if db.hasTable("client_traffics") {
		list, err := db.rows("client_traffics")
		if err != nil {
			return xrayImportSource{}, fmt.Errorf("无法读取 x-ui.db: %v", err)
		}
		for _, row := range list {
			traffic[outboundStringValue(row["email"])] = row
		}
	}

	// 3x-ui 3.x keeps clients in their own table, linked through client_inbounds.
	records := map[int64]map[string]any{}
	links := map[int64][]map[string]any{}
	if db.hasTable("clients") && db.hasTable("client_inbounds") {
		list, err := db.rows("clients")
		if err != nil {
			return xrayImportSource{}, fmt.Errorf("无法读取 x-ui.db: %v", err)
		}
		for _, row := range list {
			records[int64Value(row["id"])] = row
		}
		joins, err := db.rows("client_inbounds")
		if err != nil {
			return xrayImportSource{}, fmt.Errorf("无法读取 x-ui.db: %v", err)
		}
		for _, row := range joins {
			links[int64Value(row["inbound_id"])] = append(links[int64Value(row["inbound_id"])], row)
		}
		source.Notes = append(source.Notes, "3x-ui 3.x database: clients read from the clients table")
	}

	// 3x-ui 3.1+ keeps fallbacks in their own table instead of the settings JSON.
	fallbackMasters := map[int64]int{}
	fallbackChildren := map[int64]int64{}
	if db.hasTable("inbound_fallbacks") {
		list, err := db.rows("inbound_fallbacks")
		if err != nil {
			return xrayImportSource{}, fmt.Errorf("无法读取 x-ui.db: %v", err)
		}
		for _, row := range list {
			fallbackMasters[int64Value(row["master_id"])]++
			fallbackChildren[int64Value(row["child_id"])] = int64Value(row["master_id"])
		}
	}
	tagsByID := map[int64]string{}
	for _, row := range rows {
		tagsByID[int64Value(row["id"])] = outboundStringValue(row["tag"])
	}

	sort.SliceStable(rows, func(i, j int) bool { return int64Value(rows[i]["id"]) < int64Value(rows[j]["id"]) })
	for _, row := range rows {
		inbound := xrayImportInbound{
			FromDB:       true,
			Tag:          outboundStringValue(row["tag"]),
			Protocol:     strings.ToLower(outboundStringValue(row["protocol"])),
			Listen:       strings.TrimSpace(outboundStringValue(row["listen"])),
			Port:         row["port"],
			Settings:     jsonObject(row["settings"]),
			Stream:       jsonObject(row["stream_settings"]),
			Remark:       outboundStringValue(row["remark"]),
			Enabled:      int64Value(row["enable"]) != 0,
			Total:        uint64Value(row["total"]),
			ExpiryTime:   int64Value(row["expiry_time"]),
			Up:           uint64Value(row["up"]),
			Down:         uint64Value(row["down"]),
			TrafficReset: outboundStringValue(row["traffic_reset"]),
			// node_id (3.0+) marks an inbound pushed to a node; origin_node_guid
			// (3.3+) one synced up from a node. Neither runs on this server.
			NodeID:      (row["node_id"] != nil && int64Value(row["node_id"]) != 0) || strings.TrimSpace(outboundStringValue(row["origin_node_guid"])) != "",
			DisableFlow: row["disable_flow"] != nil && int64Value(row["disable_flow"]) != 0,
			Fallbacks:   fallbackMasters[int64Value(row["id"])],
		}
		if master, ok := fallbackChildren[int64Value(row["id"])]; ok {
			inbound.FallbackOf = tagsByID[master]
		}
		inbound.Clients = xrayClientsFromSettings(inbound.Settings)
		if joined := links[int64Value(row["id"])]; len(joined) > 0 {
			inbound.Clients = mergeXUIClientRecords(inbound.Clients, joined, records)
		}
		for index := range inbound.Clients {
			client := &inbound.Clients[index]
			if stats := traffic[client.Email]; stats != nil {
				client.Up, client.Down = uint64Value(stats["up"]), uint64Value(stats["down"])
				client.AllTime = uint64Value(stats["all_time"])
			}
		}
		source.Inbounds = append(source.Inbounds, inbound)
	}

	// Outbounds and routing live in the xray template setting.
	if db.hasTable("settings") {
		list, err := db.rows("settings")
		if err != nil {
			return xrayImportSource{}, fmt.Errorf("无法读取 x-ui.db: %v", err)
		}
		for _, row := range list {
			if outboundStringValue(row["key"]) != "xrayTemplateConfig" {
				continue
			}
			template := jsonObject(row["value"])
			if template == nil {
				source.Notes = append(source.Notes, "xrayTemplateConfig could not be parsed; outbounds and routing rules were not imported")
				break
			}
			source.Outbounds = mapList(template["outbounds"])
			if routing := mapValue(template["routing"]); routing != nil {
				source.Rules = mapList(routing["rules"])
				source.Balancers = mapList(routing["balancers"])
				source.DomainStrategy = outboundStringValue(routing["domainStrategy"])
			}
		}
	}
	if len(source.Outbounds) == 0 && len(source.Rules) == 0 {
		source.Notes = append(source.Notes, "the database has no xrayTemplateConfig: 3x-ui used its default template (direct, blocked) and default rules")
	}
	return source, nil
}

// mergeXUIClientRecords overlays 3x-ui 3.x client records on what the inbound's
// settings JSON lists, matching by email; records only in the clients table are
// added.
func mergeXUIClientRecords(clients []xrayImportClient, joins []map[string]any, records map[int64]map[string]any) []xrayImportClient {
	byEmail := map[string]int{}
	for index, client := range clients {
		byEmail[client.Email] = index
	}
	for _, join := range joins {
		record := records[int64Value(join["client_id"])]
		if record == nil {
			continue
		}
		email := outboundStringValue(record["email"])
		client := xrayImportClient{Email: email, Enabled: true}
		index, exists := byEmail[email]
		if exists {
			client = clients[index]
		}
		set := func(target *string, value any) {
			if text := outboundStringValue(value); text != "" {
				*target = text
			}
		}
		set(&client.ID, record["uuid"])
		set(&client.Password, record["password"])
		set(&client.Flow, record["flow"])
		set(&client.Auth, record["auth"])
		set(&client.Security, record["security"])
		set(&client.SubID, record["sub_id"])
		set(&client.Comment, record["comment"])
		set(&client.Flow, join["flow_override"])
		if client.Flow == "xtls-rprx-vision-udp443" {
			client.Flow = "xtls-rprx-vision"
		}
		if value, ok := record["total_gb"]; ok {
			client.TotalBytes = uint64Value(value)
		}
		if value, ok := record["expiry_time"]; ok {
			client.ExpiryTime = int64Value(value)
		}
		if value, ok := record["enable"]; ok && value != nil {
			client.Enabled = int64Value(value) != 0
		}
		if value, ok := record["limit_ip"]; ok {
			client.LimitIP = intValue(value)
		}
		set(&client.Reset, record["traffic_reset"])
		if exists {
			clients[index] = client
		} else {
			byEmail[email] = len(clients)
			clients = append(clients, client)
		}
	}
	return clients
}
