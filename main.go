package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ===== 配置结构体和文件路径 =====

const (
	ConfigFileName = "config.json"
)

// Config 结构体定义了配置文件中的字段
type Config struct {
	Port     string `json:"port"`
	APIKey   string `json:"api_key"`
	WSPath   string `json:"ws_path"`
	PushPath string `json:"push_path"` // 新增：HTTP 推送接口路径
}

// GlobalConfig 存储加载或生成的配置
var GlobalConfig Config

// ===== 默认值和环境变量获取工具 =====

// getEnv 从环境变量获取值，如果不存在，则使用默认值
func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getDefaultConfig 返回默认配置，同时考虑了环境变量
func getDefaultConfig() Config {
	return Config{
		// 默认端口 3000
		Port: getEnv("PORT", "3000"),
		// 默认 API Key
		APIKey: getEnv("RELAY_API_KEY", "U2FsdGVkX18ucQzBA+ozhc3ySrVZ"),
		// 默认 WebSocket 路径
		WSPath: getEnv("WS_PATH", "/ws"),
		// 默认 Push 接口路径
		PushPath: getEnv("PUSH_PATH", "/api/push"),
	}
}

// ⭐ 修复后的 getCurrentDir 函数：优先使用当前工作目录 (CWD)
func getCurrentDir() string {
	// 1. 优先使用 os.Getwd() 获取当前工作目录。
	//    在 GoLand 中运行时，它通常是项目根目录。
	//    在终端中运行时，它是启动程序的目录。
	dir, err := os.Getwd()
	if err == nil {
		return dir
	}

	// 2. 如果 os.Getwd() 失败，回退到执行文件所在目录（作为最终备选）
	log.Printf("⚠️ 无法获取当前工作目录，回退到执行文件所在目录: %v\n", err)
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("❌ 无法获取执行文件路径，使用 '.' (当前目录)\n")
		return "."
	}
	return filepath.Dir(execPath)
}

// loadOrCreateConfig 尝试加载配置，如果不存在则创建默认配置，并确保关键字段非空
func loadOrCreateConfig() {
	// configPath 使用 getCurrentDir() 来确定位置
	configPath := filepath.Join(getCurrentDir(), ConfigFileName)
	log.Printf("尝试从路径加载配置: %s\n", configPath)

	defaultCfg := getDefaultConfig()

	// 1. 尝试加载配置
	data, err := os.ReadFile(configPath)
	if err == nil {
		// 成功读取，解析 JSON
		if err := json.Unmarshal(data, &GlobalConfig); err != nil {
			log.Printf("⚠️ 配置解析失败，将使用默认配置！错误: %v\n", err)
			GlobalConfig = defaultCfg
		} else {
			log.Println("✅ 成功加载配置！")
		}
	} else {
		// 2. 配置不存在或读取失败，创建默认配置
		log.Printf("⚠️ 配置文件 %s 不存在或读取失败（%v），将创建默认配置！\n", ConfigFileName, err)
		GlobalConfig = defaultCfg

		// 3. 将默认配置写入文件 (只有在文件不存在时才写入)
		data, err = json.MarshalIndent(GlobalConfig, "", "  ")
		if err != nil {
			log.Printf("❌ 无法序列化默认配置: %v\n", err)
		} else {
			err = os.WriteFile(configPath, data, 0644)
			if err != nil {
				log.Printf("❌ 无法写入默认配置文件 %s: %v\n", configPath, err)
			} else {
				log.Printf("🎉 已创建默认配置文件: %s\n", configPath)
			}
		}
	}

	// 4. 配置后处理：强制检查关键字段是否为空，防止 ServeMux panic
	if GlobalConfig.Port == "" {
		GlobalConfig.Port = defaultCfg.Port
		log.Printf("⚠️ 配置中的 Port 字段为空，已回退使用默认值: %s\n", GlobalConfig.Port)
	}
	if GlobalConfig.WSPath == "" {
		GlobalConfig.WSPath = defaultCfg.WSPath
		log.Printf("⚠️ 配置中的 WSPath 字段为空，已回退使用默认值: %s\n", GlobalConfig.WSPath)
	}
	if GlobalConfig.APIKey == "" {
		GlobalConfig.APIKey = defaultCfg.APIKey
		log.Printf("⚠️ 配置中的 APIKey 字段为空，已回退使用默认值: [隐藏值]\n")
	}
	if GlobalConfig.PushPath == "" {
		GlobalConfig.PushPath = defaultCfg.PushPath
		log.Printf("⚠️ 配置中的 PushPath 字段为空，已回退使用默认值: %s\n", GlobalConfig.PushPath)
	}
}

// ===== WebSocket 客户端结构 =====

type Client struct {
	conn   *websocket.Conn
	mu     sync.Mutex // 写锁，保证多 goroutine 写同一个 conn 安全
	userID string     // 这里存的是“用户标识”，可以是 user_id 或 token 对应的id
}

// ===== 分组：所有连接 + 用户分组 =====

var (
	allClientsMu sync.RWMutex
	allClients   = make(map[*Client]struct{})

	userClientsMu sync.RWMutex
	userClients   = make(map[string]map[*Client]struct{})
)

// ===== WebSocket upgrader =====

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// 简单放行，生产可以根据域名限制
		return true
	},
}

// ===== WebSocket 消息格式 =====

type WSMessage struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

type PingMessage struct {
	Type string `json:"type"`
	Ts   int64  `json:"ts"`
}

type IdentifyData struct {
	Token string `json:"token"`
}

// 推送给前端 data 字段的结构
type Payload struct {
	Subject interface{} `json:"subject"`
	Ts      int64       `json:"ts"`
	Token   interface{} `json:"token"`
}

// HTTP /api/push 的请求体
type PushRequest struct {
	EventName    string      `json:"event_name"`
	Subject      interface{} `json:"subject"`
	DelaySeconds int         `json:"delay_seconds"`
	Token        interface{} `json:"token"`
}

// ===== 连接管理 =====

func addClient(c *Client) {
	allClientsMu.Lock()
	allClients[c] = struct{}{}
	total := len(allClients)
	allClientsMu.Unlock()

	log.Printf("🔌 新连接接入，当前 allClients 数量: %d\n", total)
}

func removeClient(c *Client) {
	allClientsMu.Lock()
	delete(allClients, c)
	allClientsMu.Unlock()

	if c.userID != "" {
		userClientsMu.Lock()
		if set, ok := userClients[c.userID]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(userClients, c.userID)
			}
		}
		userClientsMu.Unlock()
	}
}

func registerUser(c *Client, userID string) {
	if userID == "" {
		return
	}

	// 先从旧 userID 解绑
	if c.userID != "" && c.userID != userID {
		userClientsMu.Lock()
		if set, ok := userClients[c.userID]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(userClients, c.userID)
			}
		}
		userClientsMu.Unlock()
	}

	c.userID = userID

	userClientsMu.Lock()
	set, ok := userClients[userID]
	if !ok {
		set = make(map[*Client]struct{})
		userClients[userID] = set
	}
	set[c] = struct{}{}
	total := len(set)
	userClientsMu.Unlock()

	log.Printf("🆔 用户组注册完成 user_id=%s, 该用户连接数=%d\n", userID, total)
}

// ===== 发送工具（轻度优化） =====

func (c *Client) sendJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 防止写操作无限阻塞，设置一个写超时时间（比如 10 秒）
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	return c.conn.WriteJSON(v)
}

func broadcastToAll(dataObj WSMessage) {
	// 复制一份当前连接快照，避免长时间持有锁
	allClientsMu.RLock()
	if len(allClients) == 0 {
		allClientsMu.RUnlock()
		log.Println("📊 广播请求但当前无在线连接，跳过发送")
		return
	}
	clients := make([]*Client, 0, len(allClients))
	for c := range allClients {
		clients = append(clients, c)
	}
	allClientsMu.RUnlock()

	for _, c := range clients {
		if err := c.sendJSON(dataObj); err != nil {
			log.Println("🧹 广播时发送失败，清理连接:", err)
			c.conn.Close()
			removeClient(c)
		}
	}

	userClientsMu.RLock()
	userCount := len(userClients)
	userClientsMu.RUnlock()
	log.Printf("📊 广播完成：当前 allClients=%d, userClients 用户数=%d\n", len(clients), userCount)
}

func emitToUser(userID string, dataObj WSMessage) {
	userClientsMu.RLock()
	set, ok := userClients[userID]
	if !ok || len(set) == 0 {
		userClientsMu.RUnlock()
		log.Printf("🔍 未找到在线 user_id=%s，本次不推送\n", userID)
		return
	}
	clients := make([]*Client, 0, len(set))
	for c := range set {
		clients = append(clients, c)
	}
	userClientsMu.RUnlock()

	for _, c := range clients {
		if err := c.sendJSON(dataObj); err != nil {
			log.Printf("🧹 单用户推送时发送失败，清理 user_id=%s: %v\n", userID, err)
			c.conn.Close()
			removeClient(c)
		}
	}
}

// ===== WebSocket 处理 =====

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket upgrade error:", err)
		return
	}

	client := &Client{conn: conn}
	addClient(client)

	// 可选：如果你前端在 URL 上带了 ?token=xxx，这里也可以直接注册
	if token := r.URL.Query().Get("token"); token != "" {
		log.Println("🔐 连接携带 token:", token)
		registerUser(client, token)
	}

	defer func() {
		conn.Close()
		removeClient(client)
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Println("⚠️ WebSocket read error:", err)
			break
		}

		var pingMsg PingMessage
		if err := json.Unmarshal(raw, &pingMsg); err == nil && pingMsg.Type == "ping" {
			if err := conn.WriteJSON(PingMessage{Type: "pong", Ts: pingMsg.Ts}); err != nil {
				log.Println("⚠️ WebSocket pong error:", err)
				break
			}
			continue
		}

		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Println("⚠️ WebSocket message parse error:", err)
			continue
		}

		switch msg.Event {
		case "identify":
			// 解析 data.token（字符串）
			raw, _ := json.Marshal(msg.Data)
			var idData IdentifyData
			if err := json.Unmarshal(raw, &idData); err != nil {
				log.Println("identify 解析失败:", err)
				continue
			}
			if idData.Token != "" {
				log.Println("🆔 identify 收到 token:", idData.Token)
				// 直接用 token 作为分组 key
				registerUser(client, idData.Token)
			} else {
				log.Println("🆔 identify 收到空 token")
			}
		default:
			log.Printf("📨 [WS event] %s %v\n", msg.Event, msg.Data)
		}
	}
}

// ===== API KEY 中间件 (使用 GlobalConfig) =====

func checkAPIKey(next http.Handler) http.Handler {
	apiKey := GlobalConfig.APIKey // 从全局配置获取 API Key
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-KEY")
		if key == "" {
			key = r.Header.Get("API-KEY")
		}
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}

		if key == "" || key != apiKey {
			log.Println("❌ API KEY 校验失败:", key)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": -1,
				"msg":  "invalid api key",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ===== push 处理 =====

func pushHandler(w http.ResponseWriter, r *http.Request) {
	var body PushRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Println("解析 /push body 失败:", err)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": -1,
			"msg":  "invalid json",
		})
		return
	}

	log.Println("📥 [push] body =", toJSON(body))

	if body.EventName == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": -1,
			"msg":  "缺少 event_name",
		})
		return
	}

	// subject 直接透传；token 给客户端也保持原来 data.* 的位置，只是改名
	payload := Payload{
		Subject: body.Subject,
		Ts:      time.Now().UnixMilli(),
		Token:   body.Token, // ⭐ 推给前端的 data.token = token
	}

	// 用 token 做路由（实际上是用户id / 会话标识）
	targetUserId := parseUserToID(body.Token)
	log.Println("🔎 解析出的 token =", toJSON(body.Token))
	log.Println("🔎 最终 targetUserId =", targetUserId)

	dataObj := WSMessage{
		Event: body.EventName,
		Data:  payload,
	}

	doEmit := func() {
		if targetUserId != "" {
			log.Printf("🎯 单用户推送 \"%s\" 给 user_id=%s, payload=%s\n",
				body.EventName, targetUserId, toJSON(payload))
			emitToUser(targetUserId, dataObj)
		} else {
			log.Printf("🚀 广播事件 \"%s\" 给所有在线客户端, payload=%s\n",
				body.EventName, toJSON(payload))
			broadcastToAll(dataObj)
		}
	}

	delay := body.DelaySeconds
	if delay <= 0 {
		doEmit()
	} else {
		log.Printf("⏱ 计划在 %d 秒后发送事件 \"%s\"（%s）\n",
			delay,
			body.EventName,
			func() string {
				if targetUserId != "" {
					return "单用户 user_id=" + targetUserId
				}
				return "全站广播"
			}())
		go func() {
			time.Sleep(time.Duration(delay) * time.Second)
			doEmit()
		}()
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": 0,
		"msg":  "ok",
		"data": map[string]interface{}{
			"event_name":      body.EventName,
			"delay_seconds":   delay,
			"target_user_id":  targetUserId,
			"broadcast":       targetUserId == "",
			"parsed_user_raw": body.Token,
		},
	})
}

func parseUserToID(u interface{}) string {
	if u == nil {
		return ""
	}
	switch v := u.(type) {
	case float64:
		// json 数字默认 float64
		return strconv.FormatInt(int64(v), 10)
	case int, int32, int64:
		return fmt.Sprintf("%v", v)
	case string:
		if v == "" {
			return ""
		}
		return v
	case map[string]interface{}:
		if id, ok := v["id"]; ok && id != nil {
			return fmt.Sprintf("%v", id)
		}
		if id, ok := v["user_id"]; ok && id != nil {
			return fmt.Sprintf("%v", id)
		}
	}
	return ""
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ===== 入口 =====

func main() {
	// 确保配置被加载或创建，并修复了空字段问题
	loadOrCreateConfig()

	// 此时 GlobalConfig 中的所有关键字段都已填充，不会是空字符串
	port := GlobalConfig.Port
	wsPath := GlobalConfig.WSPath
	apiKey := GlobalConfig.APIKey
	pushPath := GlobalConfig.PushPath

	mux := http.NewServeMux()

	// WebSocket
	mux.HandleFunc(wsPath, wsHandler)

	// HTTP push（支持自定义路径）
	mux.Handle(pushPath, checkAPIKey(http.HandlerFunc(pushHandler)))

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	addr := ":" + port
	log.Printf("✅ Go Relay server listening on http://localhost:%s\n", port)
	log.Printf("✅ WebSocket path = %s\n", wsPath)
	log.Printf("✅ Push API path = %s\n", pushPath)
	log.Printf("✅ 使用 API_KEY = %s\n", apiKey)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
