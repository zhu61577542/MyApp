package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"myapp/internal/config"
	"myapp/internal/identity"
)

type Options struct {
	Address           string
	ConfigPath        string
	IdentityDir       string
	TrustDir          string
	DefaultDeviceName string
}

type Server struct {
	options Options
	server  *http.Server
}

func New(options Options) *Server {
	if options.Address == "" {
		options.Address = "127.0.0.1:24880"
	}
	return &Server{options: options}
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	s.server = &http.Server{Addr: s.options.Address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = s.server.Shutdown(context.Background())
	}()
	listener, err := net.Listen("tcp", s.options.Address)
	if err != nil {
		return err
	}
	return s.server.Serve(listener)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

type state struct {
	Ready       bool          `json:"ready"`
	ConfigPath  string        `json:"config_path"`
	DeviceID    string        `json:"device_id,omitempty"`
	DeviceName  string        `json:"device_name,omitempty"`
	Role        config.Role   `json:"role,omitempty"`
	Listen      string        `json:"listen_address,omitempty"`
	Peers       []config.Peer `json:"peers,omitempty"`
	Clipboard   bool          `json:"clipboard_enabled"`
	Files       bool          `json:"files_enabled"`
	SetupReason string        `json:"setup_reason,omitempty"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	result := state{ConfigPath: s.options.ConfigPath}
	cfg, cfgErr := config.Load(s.options.ConfigPath)
	identityValue, identityErr := identity.Load(s.options.IdentityDir)
	if cfgErr != nil || identityErr != nil {
		result.SetupReason = setupReason(cfgErr, identityErr)
		writeJSON(w, result)
		return
	}
	result.Ready = true
	result.DeviceID = identityValue.DeviceID
	result.DeviceName = cfg.DeviceName
	result.Role = cfg.Role
	result.Listen = cfg.ListenAddress
	result.Peers = cfg.Peers
	result.Clipboard = cfg.Clipboard.TextEnabled
	result.Files = cfg.Files.Enabled
	writeJSON(w, result)
}

type bootstrapRequest struct {
	DeviceName string      `json:"device_name"`
	Role       config.Role `json:"role"`
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var request bootstrapRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求格式无效")
		return
	}
	request.DeviceName = strings.TrimSpace(request.DeviceName)
	if request.DeviceName == "" {
		request.DeviceName = s.options.DefaultDeviceName
	}
	if request.Role == "" {
		request.Role = config.RoleAgent
	}
	if request.Role != config.RoleAgent && request.Role != config.RoleController {
		writeJSONError(w, http.StatusBadRequest, "角色必须是 controller 或 agent")
		return
	}
	if _, err := os.Stat(s.options.ConfigPath); err == nil {
		writeJSONError(w, http.StatusConflict, "配置已经存在")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := identity.Load(s.options.IdentityDir); err == nil {
		writeJSONError(w, http.StatusConflict, "身份已经存在")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	generated, err := identity.Generate(request.DeviceName, time.Now())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := identity.SaveNew(s.options.IdentityDir, generated); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := config.SaveNew(s.options.ConfigPath, config.Default(request.DeviceName, request.Role)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"device_id": generated.DeviceID})
}

func setupReason(cfgErr, identityErr error) string {
	switch {
	case errors.Is(cfgErr, os.ErrNotExist) && errors.Is(identityErr, os.ErrNotExist):
		return "尚未初始化设备身份和配置"
	case errors.Is(cfgErr, os.ErrNotExist):
		return "尚未初始化配置"
	case errors.Is(identityErr, os.ErrNotExist):
		return "尚未初始化设备身份"
	default:
		return "配置或身份文件无效，请查看 CLI 错误信息"
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	writeJSON(w, map[string]string{"error": message})
}

func Fingerprint(deviceID string) string {
	sum := sha256.Sum256([]byte(deviceID))
	return hex.EncodeToString(sum[:])[:16]
}

func AddressHint(address string) string {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Sprintf("地址无效: %s", address)
	}
	return filepath.Clean(address)
}

const indexHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>MyApp 控制中心</title>
<style>
:root{color-scheme:dark;font-family:Inter,system-ui,sans-serif;background:#10151f;color:#e8eef7}body{margin:0;background:linear-gradient(135deg,#10151f,#18253a);min-height:100vh}.wrap{max-width:980px;margin:auto;padding:42px 22px}.brand{display:flex;justify-content:space-between;align-items:center;margin-bottom:28px}.brand h1{margin:0;font-size:30px}.pill{padding:7px 12px;border-radius:999px;background:#293750;color:#a9c8ff;font-size:13px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px}.card{background:#192437;border:1px solid #2c3c58;border-radius:16px;padding:20px;box-shadow:0 12px 30px #080c1440}.card h2{font-size:16px;margin:0 0 15px;color:#b9c8df}.value{font-size:22px;font-weight:650;margin:8px 0;overflow-wrap:anywhere}.muted{color:#91a0b8;font-size:13px;line-height:1.6}.peer{border-top:1px solid #2c3c58;padding:12px 0}.peer:first-child{border-top:0}.row{display:flex;justify-content:space-between;gap:12px}.button{border:0;border-radius:10px;padding:10px 14px;background:#5c8dff;color:#fff;font-weight:650;cursor:pointer}.button:disabled{opacity:.5}.form{display:grid;gap:12px}.form input,.form select{border:1px solid #3b4e6d;border-radius:9px;background:#111a2a;color:#fff;padding:11px}.notice{padding:14px;border-radius:10px;background:#3a2d1d;color:#ffdca2;margin-bottom:16px}.hidden{display:none}.mono{font-family:ui-monospace,monospace;font-size:12px;color:#a9c8ff}
</style></head><body><main class="wrap"><div class="brand"><h1>MyApp 控制中心</h1><span id="status" class="pill">正在读取状态</span></div><div id="setup" class="card hidden"><h2>首次启动</h2><p class="muted">还没有本机配置。创建身份和配置后，可以继续配对其他设备。</p><form id="setupForm" class="form"><input id="deviceName" placeholder="设备名称" maxlength="63"><select id="role"><option value="agent">受控电脑（agent）</option><option value="controller">主电脑（controller）</option></select><button class="button">初始化 MyApp</button></form></div><div id="app" class="hidden"><div class="grid"><section class="card"><h2>本机</h2><div id="device" class="value">—</div><div id="id" class="mono">—</div><p id="roleText" class="muted">—</p></section><section class="card"><h2>能力</h2><div class="row"><span>文字剪贴板</span><strong id="clipboard">—</strong></div><div class="row"><span>文件复制</span><strong id="files">—</strong></div><p id="listen" class="muted">—</p></section></div><section class="card" style="margin-top:16px"><h2>已配对设备</h2><div id="peers" class="muted">暂无设备</div></section></div><div id="error" class="notice hidden"></div></main>
<script>
const $=id=>document.getElementById(id); const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
async function load(){try{const r=await fetch('/api/state');const s=await r.json();if(!s.ready){$('setup').classList.remove('hidden');$('status').textContent='需要初始化';return}$('app').classList.remove('hidden');$('status').textContent='配置已就绪';$('device').textContent=s.device_name;$('id').textContent=s.device_id;$('roleText').textContent=s.role==='controller'?'主电脑：负责连接受控电脑':'受控电脑：等待主电脑连接';$('clipboard').textContent=s.clipboard_enabled?'已启用':'已暂停';$('files').textContent=s.files_enabled?'已启用':'已暂停';$('listen').textContent='监听地址：'+s.listen_address;const peers=s.peers||[];$('peers').innerHTML=peers.length?peers.map(p=>'<div class="peer"><div class="row"><strong>'+esc(p.id.slice(0,12)+'…')+'</strong><span>'+esc(p.address)+'</span></div></div>').join(''):'暂无设备，请先通过 CLI 配对';}catch(e){$('error').classList.remove('hidden');$('error').textContent='无法读取状态：'+e.message;$('status').textContent='连接失败';}}
$('setupForm').addEventListener('submit',async e=>{e.preventDefault();const b=$('setupForm').querySelector('button');b.disabled=true;try{const r=await fetch('/api/bootstrap',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({device_name:$('deviceName').value,role:$('role').value})});if(!r.ok){const x=await r.json();throw new Error(x.error||'初始化失败')}await load()}catch(e){$('error').classList.remove('hidden');$('error').textContent=e.message}finally{b.disabled=false}});load();setInterval(load,5000);
</script></body></html>`
