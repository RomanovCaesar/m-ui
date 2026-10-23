# Generates xui_v2.db (3x-ui 2.x layout) and xui_v3.db (3x-ui 3.x layout) for
# TestXUIDatabaseImport. Run: python -I gen_xui_db.py  (from this directory)
import json, os, sqlite3

here = os.path.dirname(os.path.abspath(__file__))
PRIVATE = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

TEMPLATE = {
    "outbounds": [
        {"tag": "direct", "protocol": "freedom", "settings": {"domainStrategy": "AsIs"}},
        {"tag": "blocked", "protocol": "blackhole", "settings": {}},
        {"tag": "upstream", "protocol": "trojan",
         "settings": {"servers": [{"address": "up.example.net", "port": 443, "password": "up-pass"}]},
         "streamSettings": {"network": "tcp", "security": "tls", "tlsSettings": {"serverName": "up.example.net"}}},
    ],
    "routing": {"domainStrategy": "IPIfNonMatch", "rules": [
        {"type": "field", "inboundTag": ["api"], "outboundTag": "api"},
        {"type": "field", "outboundTag": "blocked", "ip": ["geoip:private"]},
        {"type": "field", "user": ["vip@main"], "domain": ["geosite:netflix"], "outboundTag": "upstream"},
    ]},
}

def reality_stream():
    return json.dumps({"network": "tcp", "security": "reality", "realitySettings": {
        "target": "www.apple.com:443", "serverNames": ["www.apple.com"], "privateKey": PRIVATE, "shortIds": ["ab"]}})

def base_tables(db, v3):
    extra = ", `node_id` integer, `traffic_reset_day` integer DEFAULT 1, `disable_flow` numeric DEFAULT false, `origin_node_guid` text" if v3 else ""
    db.execute("CREATE TABLE `inbounds` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` integer,`up` integer,`down` integer,`total` integer,"
               "`all_time` integer DEFAULT 0,`remark` text,`enable` numeric,`expiry_time` integer,`traffic_reset` text DEFAULT \"never\","
               "`last_traffic_reset_time` integer DEFAULT 0,`listen` text,`port` integer,`protocol` text,`settings` text,`stream_settings` text,"
               "`tag` text,`sniffing` text" + extra + ",CONSTRAINT `uni_inbounds_tag` UNIQUE (`tag`))")
    db.execute("CREATE TABLE `client_traffics` (`id` integer PRIMARY KEY AUTOINCREMENT,`inbound_id` integer,`enable` numeric,`email` text,"
               "`up` integer,`down` integer,`all_time` integer,`expiry_time` integer,`total` integer,`reset` integer DEFAULT 0,`last_online` integer DEFAULT 0)")
    db.execute("CREATE TABLE `settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`key` text,`value` text)")
    db.execute("INSERT INTO settings(key,value) VALUES ('xrayTemplateConfig', ?)", (json.dumps(TEMPLATE),))
    db.execute("INSERT INTO settings(key,value) VALUES ('webPort', '2053')")

def make_v2(path):
    db = sqlite3.connect(path)
    base_tables(db, False)
    clients = [
        {"id": "11111111-1111-4111-8111-111111111111", "flow": "xtls-rprx-vision", "email": "vip@main", "limitIp": 2,
         "totalGB": 107374182400, "expiryTime": 1893456000000, "enable": True, "subId": "abcdefgh12345678", "comment": "paid until 2030"},
        {"id": "22222222-2222-4222-8222-222222222222", "flow": "", "email": "trial@main",
         "totalGB": 0, "expiryTime": -604800000, "enable": True, "subId": "trialsub00000001", "comment": "", "reset": 30},
        {"id": "33333333-3333-4333-8333-333333333333", "flow": "", "email": "banned@main",
         "totalGB": 0, "expiryTime": 0, "enable": False, "subId": "", "comment": "disabled"},
    ]
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing) "
               "VALUES (1,1000,2000,0,'Main Reality',1,0,'monthly','',443,'vless',?,?,'inbound-443','{}')",
               (json.dumps({"clients": clients, "decryption": "none", "fallbacks": []}), reality_stream()))
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing) "
               "VALUES (1,0,0,0,'Paused',0,0,'never','',8080,'vmess',?,?,'inbound-8080','{}')",
               (json.dumps({"clients": [{"id": "44444444-4444-4444-8444-444444444444", "email": "vip@second", "subId": "abcdefgh12345678", "enable": True, "totalGB": 0, "expiryTime": 0}]}),
                json.dumps({"network": "ws", "security": "none", "wsSettings": {"path": "/ws"}})))
    for email, up, down, all_time in [("vip@main", 5_000_000, 7_000_000, 20_000_000), ("trial@main", 10, 20, 30), ("vip@second", 1, 2, 3)]:
        db.execute("INSERT INTO client_traffics(inbound_id,enable,email,up,down,all_time,expiry_time,total) VALUES (1,1,?,?,?,?,0,0)", (email, up, down, all_time))
    db.commit(); db.execute("VACUUM"); db.close()

def make_v3(path):
    db = sqlite3.connect(path)
    base_tables(db, True)
    db.execute("CREATE TABLE `clients` (`id` integer PRIMARY KEY AUTOINCREMENT,`email` text NOT NULL,`sub_id` text,`uuid` text,`password` text,`auth` text,"
               "`flow` text,`security` text,`reverse` text,`limit_ip` integer,`total_gb` integer,`expiry_time` integer,`enable` numeric DEFAULT true,"
               "`tg_id` integer,`group_name` text DEFAULT '',`comment` text,`reset` integer DEFAULT 0,`traffic_reset` text DEFAULT 'never',`traffic_reset_day` integer DEFAULT 1,`reset_day` integer DEFAULT 0,`reset_weekday` integer DEFAULT 0,`reset_max` integer DEFAULT 0,`created_at` integer,`updated_at` integer)")
    db.execute("CREATE TABLE `client_inbounds` (`client_id` integer,`inbound_id` integer,`flow_override` text,`created_at` integer,PRIMARY KEY (`client_id`,`inbound_id`))")
    # In 3.x the settings JSON may still list clients, with stale limits; the clients table wins.
    stale = [{"id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "email": "anna", "totalGB": 1, "expiryTime": 1, "enable": True}]
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing,node_id) "
               "VALUES (1,0,0,53687091200,'V3 Reality',1,1900000000000,'never','',443,'vless',?,?,'in-443-tcp','{}',NULL)",
               (json.dumps({"clients": stale, "decryption": "none"}), reality_stream()))
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing,node_id) "
               "VALUES (1,0,0,0,'On node',1,0,'never','',8443,'vless',?,?,'in-8443-node','{}',7)",
               (json.dumps({"clients": [], "decryption": "none"}), reality_stream()))
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing,node_id) "
               "VALUES (1,0,0,0,'TUIC',1,0,'never','',9443,'tuic',?,'','in-9443-tuic','{}',NULL)",
               (json.dumps({"server": {"certificate": "/etc/x-ui/cert.pem", "private_key": "/etc/x-ui/key.pem", "congestion_control": "bbr", "alpn": ["h3"]},
                            "clients": [{"uuid": "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "password": "tuic-pass", "email": "tina"}]}),))
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing,node_id) "
               "VALUES (1,0,0,0,'MTProto',1,0,'never','',8888,'mtproto','{}','','in-8888-mt','{}',NULL)")
    # 3.7+: disable_flow drops the clients' flow; 3.3+: origin_node_guid marks an inbound synced from a node;
    # 3.1+: fallbacks live in inbound_fallbacks (master 1 -> child 6 on loopback).
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing,disable_flow) "
               "VALUES (1,0,0,0,'No Flow',1,0,'never','',8444,'vless',?,?,'in-8444-noflow','{}',1)",
               (json.dumps({"clients": [{"id": "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "email": "dora", "flow": "xtls-rprx-vision", "enable": True}], "decryption": "none"}), reality_stream()))
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing,origin_node_guid) "
               "VALUES (1,0,0,0,'Synced',1,0,'never','',8445,'vless',?,?,'in-8445-synced','{}','5d1f0c2e-node')",
               (json.dumps({"clients": [], "decryption": "none"}), reality_stream()))
    db.execute("INSERT INTO inbounds(user_id,up,down,total,remark,enable,expiry_time,traffic_reset,listen,port,protocol,settings,stream_settings,tag,sniffing) "
               "VALUES (1,0,0,0,'WS child',1,0,'never','127.0.0.1',20001,'vless',?,?,'in-20001-ws','{}')",
               (json.dumps({"clients": [{"id": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "email": "eve", "flow": "xtls-rprx-vision", "enable": True}], "decryption": "none"}),
                json.dumps({"network": "ws", "security": "none", "wsSettings": {"path": "/child"}})))
    db.execute("CREATE TABLE `inbound_fallbacks` (`id` integer PRIMARY KEY AUTOINCREMENT,`master_id` integer NOT NULL,`child_id` integer NOT NULL,"
               "`name` text,`alpn` text,`path` text,`dest` text,`xver` integer,`sort_order` integer DEFAULT 0)")
    db.execute("INSERT INTO inbound_fallbacks(master_id,child_id,path,dest,xver) VALUES (1,7,'/child','127.0.0.1:20001',0)")
    db.execute("INSERT INTO clients(email,sub_id,uuid,flow,limit_ip,total_gb,expiry_time,enable,comment) VALUES ('anna','annasub000000001','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','',0,214748364800,1950000000000,1,'from clients table')")
    db.execute("INSERT INTO clients(email,sub_id,uuid,flow,limit_ip,total_gb,expiry_time,enable,comment) VALUES ('ben','bensub0000000001','bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','',0,0,0,0,'only in clients table')")
    db.execute("UPDATE clients SET traffic_reset='monthly', traffic_reset_day=15 WHERE email='ben'")
    db.execute("UPDATE clients SET reset_day=1, reset_max=12 WHERE email='anna'")
    db.execute("ALTER TABLE client_traffics ADD COLUMN reset_count integer DEFAULT 0")
    db.execute("UPDATE inbounds SET traffic_reset='monthly', traffic_reset_day=20 WHERE tag='in-443-tcp'")
    db.execute("INSERT INTO client_inbounds(client_id,inbound_id,flow_override) VALUES (1,1,'xtls-rprx-vision'),(2,1,'')")
    db.execute("INSERT INTO client_traffics(inbound_id,enable,email,up,down,all_time,expiry_time,total,reset_count) VALUES (1,1,'anna',100,200,300,0,0,3)")
    db.commit(); db.execute("VACUUM"); db.close()

for name, maker in (("xui_v2.db", make_v2), ("xui_v3.db", make_v3)):
    path = os.path.join(here, name)
    if os.path.exists(path):
        os.remove(path)
    maker(path)
    print(name, os.path.getsize(path), "bytes")
