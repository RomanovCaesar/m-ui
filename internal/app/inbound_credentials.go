package app

import "reflect"

// Imported inbounds (Mihomo YAML, Xray / 3x-ui) keep their original listener in
// Inbound.Native so options the form does not know survive. Who may connect is
// never taken from there: the client list is the authority, otherwise renaming,
// adding, disabling or limiting a client would not reach Mihomo, and a listener
// imported without usernames would report no InUser for traffic accounting.

// inboundCredentialKeys lists the listener keys that hold the credentials.
func inboundCredentialKeys(inboundType string) []string {
	switch inboundType {
	case "redir", "tproxy", "hysteria2-realm":
		return nil
	case "shadowsocks":
		return []string{"users", "password"}
	case "snell":
		return []string{"psk"}
	case "sudoku":
		return []string{"key"}
	}
	return []string{"users"}
}

// applyInboundCredentials replaces the credentials of a native listener with the
// ones of the inbound's enabled clients.
func applyInboundCredentials(native map[string]any, inbound Inbound) error {
	keys := inboundCredentialKeys(inbound.Type)
	// Without clients there is nothing to manage: an open mixed port, or a
	// listener whose credentials m-ui does not model (a TUIC v4 token).
	if len(keys) == 0 || len(inbound.Clients) == 0 {
		return nil
	}
	credentials := Inbound{Type: inbound.Type, Name: inbound.Name, Port: inbound.Port, Cipher: inbound.Cipher, Clients: inbound.Clients,
		Username: inbound.Username, Password: inbound.Password, UUID: inbound.UUID,
		SimpleAuthEnabled: inbound.SimpleAuthEnabled, SimpleAuthConfigured: inbound.SimpleAuthConfigured}
	managed, err := managedInboundConfig(credentials)
	if err != nil {
		return err
	}
	for _, key := range keys {
		value, ok := managed[key]
		if !ok {
			delete(native, key)
			continue
		}
		if users, ok := value.([]map[string]any); ok {
			value = keepNativeUserOptions(native[key], users)
		}
		native[key] = value
	}
	return nil
}

// keepNativeUserOptions carries per-user options m-ui does not model over from
// the imported entry with the same UUID, or the same name and password.
func keepNativeUserOptions(native any, users []map[string]any) []map[string]any {
	entries, _ := native.([]any)
	result := make([]map[string]any, 0, len(users))
	for _, user := range users {
		merged := map[string]any{}
		for _, value := range entries {
			entry := mapValue(value)
			if entry == nil || !sameNativeUser(entry, user) {
				continue
			}
			for key, option := range entry {
				merged[key] = option
			}
			break
		}
		for key, option := range user {
			merged[key] = option
		}
		result = append(result, merged)
	}
	return result
}

func sameNativeUser(entry, user map[string]any) bool {
	if uuid, ok := user["uuid"]; ok {
		return reflect.DeepEqual(entry["uuid"], uuid)
	}
	return reflect.DeepEqual(entry["username"], user["username"]) && reflect.DeepEqual(entry["password"], user["password"])
}

// inboundLocksEveryoneOut reports whether the inbound authenticates its clients
// and none of them may connect. Such a listener is left out of the config:
// rendering it would need a placeholder user (a fixed UUID, an empty password)
// and, for mixed, SOCKS and HTTP, an empty list, which Mihomo reads as no
// authentication at all.
func inboundLocksEveryoneOut(inbound Inbound) bool {
	if len(inboundCredentialKeys(inbound.Type)) == 0 || len(enabledClients(inbound)) > 0 {
		return false
	}
	switch inbound.Type {
	case "shadowsocks", "snell", "sudoku":
		return true
	case "mixed", "socks", "http":
		if len(inbound.Clients) == 0 {
			return false
		}
		return inbound.SimpleAuthEnabled || !inbound.SimpleAuthConfigured
	}
	if len(inbound.Clients) > 0 {
		return true
	}
	if inbound.Native != nil {
		// The last client was deleted, but its imported entry is still there.
		_, hasUsers := inbound.Native["users"]
		return hasUsers
	}
	return inbound.Username == "" && inbound.Password == "" && inbound.UUID == ""
}
