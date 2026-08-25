package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	AdminUser     = getEnv("ADMIN_USER", "")
	AdminPassword = getEnv("ADMIN_PASSWORD", "")
	SessionCookie = getEnv("SESSION_COOKIE", "")
	SessionValue  = getEnv("SESSION_VALUE", "")

	DirectHost    = getEnv("DIRECT_HOST", "")
	SSHPort       = getEnv("INCOMMING_PORT", "")
	UsersDir      = "/etc/vpn_users"

	TelegramBotToken = getEnv("TELEGRAM_BOT_TOKEN", "")
	TelegramAdminID  = getEnvInt64("TELEGRAM_ADMIN_ID", 0)
)

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intVal
		}
	}
	return fallback
}

type UserInfo struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	UsedMB        int    `json:"used_mb"`
	LimitMB       int    `json:"limit_mb"`
	Percent       int    `json:"percent"`
	ExpireDate    string `json:"expire_date"`
	MaxConn       string `json:"max_conn"`
	ActiveConn    int    `json:"active_conn"`
	IsLocked      bool   `json:"is_locked"`
	NpvLink       string `json:"npv_link"`
	StreisandLink string `json:"streisand_link"`
}

type PageData struct {
	Users         []UserInfo
	SSHPort       string
	DirectHost    string
	TotalUsers    int
	ActiveUsers   int
	LockedUsers   int
	TotalUsedGB   string
	CPUUsage      string
	RAMUsage      string
	SSHStatus     string
}

type LoginData struct {
	Error string
}

func readUserFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func getUsersData() []UserInfo {
	var users []UserInfo

	limitFiles, _ := filepath.Glob(filepath.Join(UsersDir, "*.limit"))
	lockedFiles, _ := filepath.Glob(filepath.Join(UsersDir, "*.locked"))

	allUsernames := make(map[string]bool)
	isLockedMap := make(map[string]bool)

	for _, f := range limitFiles {
		u := strings.TrimSuffix(filepath.Base(f), ".limit")
		allUsernames[u] = true
	}
	for _, f := range lockedFiles {
		u := strings.TrimSuffix(filepath.Base(f), ".locked")
		allUsernames[u] = true
		isLockedMap[u] = true
	}

	for u := range allUsernames {
		pass := readUserFile(filepath.Join(UsersDir, u+".auth"))
		expire := readUserFile(filepath.Join(UsersDir, u+".expire"))
		if expire == "" {
			expire = "نامحدود"
		}

		maxConn := readUserFile(filepath.Join(UsersDir, u+".maxconn"))
		if maxConn == "" {
			maxConn = "1"
		}

		usedBytesStr := readUserFile(filepath.Join(UsersDir, u+".used"))
		usedBytes, _ := strconv.Atoi(usedBytesStr)
		usedMB := usedBytes / 1024 / 1024

		var limitMB int
		if isLockedMap[u] {
			limitMB = usedMB
		} else {
			limitBytesStr := readUserFile(filepath.Join(UsersDir, u+".limit"))
			limitBytes, _ := strconv.Atoi(limitBytesStr)
			limitMB = limitBytes / 1024 / 1024
		}

		percent := 0
		if limitMB > 0 {
			percent = (usedMB * 100) / limitMB
			if percent > 100 {
				percent = 100
			}
		}

		cmd := fmt.Sprintf("ps -ef | grep '[s]shd-session: %s \\[priv\\]' | wc -l", u)
		out, _ := exec.Command("bash", "-c", cmd).Output()
		activeCount, _ := strconv.Atoi(strings.TrimSpace(string(out)))

		npvJSON := fmt.Sprintf(`{"type":"ssh","name":"%s","server":"%s","port":%s,"user":"%s","password":"%s","udp":true}`,
			u, DirectHost, SSHPort, u, pass)
		npvLink := "npv://" + base64.StdEncoding.EncodeToString([]byte(npvJSON))

		streisandLink := fmt.Sprintf("ssh://%s:%s@%s:%s#%s", u, pass, DirectHost, SSHPort, u)

		users = append(users, UserInfo{
			Username:      u,
			Password:      pass,
			UsedMB:        usedMB,
			LimitMB:       limitMB,
			Percent:       percent,
			ExpireDate:    expire,
			MaxConn:       maxConn,
			ActiveConn:    activeCount,
			IsLocked:      isLockedMap[u],
			NpvLink:       npvLink,
			StreisandLink: streisandLink,
		})
	}

	return users
}

func sendTelegramMessage(chatID int64, text string) {
	if TelegramBotToken == "" || chatID == 0 {
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TelegramBotToken)
	payload, _ := json.Marshal(map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	http.Post(url, "application/json", bytes.NewBuffer(payload))
}

func sendTelegramDocument(chatID int64, filePath, caption string) {
	if TelegramBotToken == "" || chatID == 0 {
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("document", filepath.Base(filePath))
	if err == nil {
		io.Copy(part, file)
	}
	writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	writer.WriteField("caption", caption)
	writer.Close()

	req, _ := http.NewRequest("POST", fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", TelegramBotToken), body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	http.DefaultClient.Do(req)
}

func startBackupScheduler() {
	if TelegramBotToken == "" || TelegramAdminID == 0 {
		return
	}
	for {
		time.Sleep(24 * time.Hour)
		backupPath := "/tmp/vpn_users_backup.tar.gz"
		cmd := exec.Command("tar", "-czf", backupPath, "-C", "/etc", "vpn_users")
		if err := cmd.Run(); err == nil {
			caption := fmt.Sprintf("📦 <b>بکاپ خودکار روزانه کاربران</b>\n📅 تاریخ: %s", time.Now().Format("2006-01-02 15:04"))
			sendTelegramDocument(TelegramAdminID, backupPath, caption)
			os.Remove(backupPath)
		}
	}
}

func startTelegramBot() {
	if TelegramBotToken == "" {
		return
	}
	offset := 0
	for {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", TelegramBotToken, offset)
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var tgResp struct {
			Ok     bool `json:"ok"`
			Result []struct {
				UpdateID int `json:"update_id"`
				Message  *struct {
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				} `json:"message"`
			} `json:"result"`
		}

		if err := json.Unmarshal(body, &tgResp); err != nil || !tgResp.Ok {
			time.Sleep(5 * time.Second)
			continue
		}

		for _, update := range tgResp.Result {
			offset = update.UpdateID + 1
			if update.Message == nil {
				continue
			}

			chatID := update.Message.Chat.ID
			text := strings.TrimSpace(update.Message.Text)

			if TelegramAdminID != 0 && chatID != TelegramAdminID {
				sendTelegramMessage(chatID, "⛔ شما اجازه دسترسی ندارید.")
				continue
			}

			parts := strings.Fields(text)
			if len(parts) == 0 {
				continue
			}

			switch parts[0] {
			case "/start", "/help":
				sendTelegramMessage(chatID, `⚡ <b>راهنمای ربات SSH VPN:</b>

📊 <code>/status</code> - وضعیت زنده کاربران
➕ <code>/add user pass limit_MB days maxconn</code> - ساخت کاربر
🔄 <code>/update user new_limit_MB</code> - تمدید حجم
❌ <code>/delete user</code> - حذف کاربر
📦 <code>/backup</code> - دریافت فایل بکاپ`)

			case "/status":
				users := getUsersData()
				if len(users) == 0 {
					sendTelegramMessage(chatID, "هیچ کاربری یافت نشد.")
					continue
				}
				var sb strings.Builder
				sb.WriteString("📊 <b>وضعیت لحظه‌ای کاربران:</b>\n\n")
				for _, u := range users {
					status := "🟢 فعال"
					if u.IsLocked {
						status = "🔴 مسدود"
					}
					sb.WriteString(fmt.Sprintf("👤 <b>%s</b> | %s\n", u.Username, status))
					sb.WriteString(fmt.Sprintf("💾 مصرف: %d / %d MB (%d%%)\n", u.UsedMB, u.LimitMB, u.Percent))
					sb.WriteString(fmt.Sprintf("⏳ انقضا: %s | آنلاین: %d/%s\n\n", u.ExpireDate, u.ActiveConn, u.MaxConn))
				}
				sendTelegramMessage(chatID, sb.String())

			case "/backup":
				backupPath := "/tmp/manual_backup.tar.gz"
				exec.Command("tar", "-czf", backupPath, "-C", "/etc", "vpn_users").Run()
				sendTelegramDocument(chatID, backupPath, "📦 فایل پشتیبان کاربران")
				os.Remove(backupPath)

			case "/add":
				if len(parts) < 6 {
					sendTelegramMessage(chatID, "⚠️ فرمت صحیح:\n<code>/add user pass limit_MB days maxconn</code>")
					continue
				}
				u, p, limit, days, maxconn := parts[1], parts[2], parts[3], parts[4], parts[5]
				exec.Command("/usr/local/bin/add_user", u, p, limit, days, maxconn).Run()

				npvJSON := fmt.Sprintf(`{"type":"ssh","name":"%s","server":"%s","port":%s,"user":"%s","password":"%s","udp":true}`,
					u, DirectHost, SSHPort, u, p)
				npvLink := "npv://" + base64.StdEncoding.EncodeToString([]byte(npvJSON))
				streisandLink := fmt.Sprintf("ssh://%s:%s@%s:%s#%s", u, p, DirectHost, SSHPort, u)

				reply := fmt.Sprintf("✅ <b>کاربر %s ساخته شد.</b>\n\n"+
					"📱 <b>NapsternetV:</b>\n<code>%s</code>\n\n"+
					"🍎 <b>Streisand:</b>\n<code>%s</code>", u, npvLink, streisandLink)
				sendTelegramMessage(chatID, reply)

			case "/update":
				if len(parts) < 3 {
					sendTelegramMessage(chatID, "⚠️ فرمت:\n<code>/update user new_limit_MB</code>")
					continue
				}
				u, limitMBStr := parts[1], parts[2]
				limitMB, _ := strconv.Atoi(limitMBStr)
				limitBytes := limitMB * 1024 * 1024
				exec.Command("bash", "-c", "echo "+strconv.Itoa(limitBytes)+" > /etc/vpn_users/"+u+".limit").Run()
				exec.Command("bash", "-c", "rm -f /etc/vpn_users/"+u+".locked").Run()
				exec.Command("usermod", "-U", u).Run()
				sendTelegramMessage(chatID, fmt.Sprintf("✅ حجم کاربر <b>%s</b> به <b>%d MB</b> افزایش یافت.", u, limitMB))

			case "/delete":
				if len(parts) < 2 {
					sendTelegramMessage(chatID, "⚠️ فرمت:\n<code>/delete user</code>")
					continue
				}
				u := parts[1]
				exec.Command("pkill", "-u", u).Run()
				exec.Command("userdel", "-f", u).Run()
				exec.Command("bash", "-c", "rm -f /etc/vpn_users/"+u+".*").Run()
				sendTelegramMessage(chatID, fmt.Sprintf("🗑️ کاربر <b>%s</b> حذف شد.", u))
			}
		}
	}
}

const loginTemplate = `
<!DOCTYPE html>
<html lang="fa" dir="rtl">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ورود به پنل مدیریت SSH</title>
    <link href="https://cdn.jsdelivr.net/gh/rastikerdar/vazirmatn@v33.003/Vazirmatn-font-face.css" rel="stylesheet" type="text/css" />
    <style>
        :root { --bg-color: #0b0f19; --card-bg: rgba(23, 32, 54, 0.85); --border-color: rgba(255, 255, 255, 0.08); --primary: #3b82f6; --danger: #ef4444; }
        * { box-sizing: border-box; margin: 0; padding: 0; font-family: 'Vazirmatn', sans-serif; }
        body { background: radial-gradient(circle at center, #1e293b 0%, var(--bg-color) 90%); color: #fff; min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 20px; }
        .login-card { background: var(--card-bg); border: 1px solid var(--border-color); border-radius: 16px; padding: 30px; width: 100%; max-width: 400px; backdrop-filter: blur(16px); box-shadow: 0 10px 30px rgba(0,0,0,0.5); }
        h2 { font-size: 20px; margin-bottom: 20px; text-align: center; }
        .form-group { margin-bottom: 15px; }
        label { display: block; font-size: 13px; color: #94a3b8; margin-bottom: 6px; }
        input { width: 100%; padding: 11px 14px; background: rgba(11, 15, 25, 0.9); border: 1px solid var(--border-color); border-radius: 8px; color: #fff; outline: none; }
        input:focus { border-color: var(--primary); }
        .btn { width: 100%; padding: 11px; background: var(--primary); border: none; border-radius: 8px; color: #fff; font-weight: 600; cursor: pointer; margin-top: 10px; }
        .error { color: var(--danger); font-size: 13px; margin-bottom: 12px; text-align: center; }
    </style>
</head>
<body>
    <div class="login-card">
        <h2>🔒 ورود به پنل SSH</h2>
        {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
        <form method="POST" action="/login">
            <div class="form-group">
                <label>نام کاربری مدیر</label>
                <input type="text" name="username" required autocomplete="off">
            </div>
            <div class="form-group">
                <label>رمز عبور مدیر</label>
                <input type="password" name="password" required>
            </div>
            <button class="btn" type="submit">ورود</button>
        </form>
    </div>
</body>
</html>
`

const htmlTemplate = `
<!DOCTYPE html>
<html lang="fa" dir="rtl">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>مدیریت سرور SSH VPN</title>
    <link href="https://cdn.jsdelivr.net/gh/rastikerdar/vazirmatn@v33.003/Vazirmatn-font-face.css" rel="stylesheet" type="text/css" />
    <style>
        :root {
            --bg-color: #0b0f19;
            --card-bg: rgba(23, 32, 54, 0.75);
            --border-color: rgba(255, 255, 255, 0.08);
            --primary: #3b82f6;
            --success: #10b981;
            --warning: #f59e0b;
            --danger: #ef4444;
            --text-main: #f8fafc;
            --text-muted: #94a3b8;
        }

        * { box-sizing: border-box; margin: 0; padding: 0; font-family: 'Vazirmatn', sans-serif; }
        body { background: radial-gradient(circle at 10% 20%, #1e293b 0%, var(--bg-color) 90%); color: var(--text-main); padding: 30px 20px; min-height: 100vh; }
        .container { max-width: 1300px; margin: 0 auto; }
        
        header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 25px; padding-bottom: 15px; border-bottom: 1px solid var(--border-color); }
        header h1 { font-size: 20px; font-weight: 700; }
        .header-actions { display: flex; align-items: center; gap: 10px; }
        .badge { background: rgba(59, 130, 246, 0.15); color: var(--primary); border: 1px solid rgba(59, 130, 246, 0.3); padding: 5px 12px; border-radius: 20px; font-size: 12px; }
        .logout-btn { color: var(--danger); text-decoration: none; font-size: 13px; border: 1px solid rgba(239, 68, 68, 0.3); padding: 5px 12px; border-radius: 8px; transition: 0.2s; }
        .logout-btn:hover { background: rgba(239, 68, 68, 0.1); }

        .dashboard-grid { display: grid; grid-template-columns: 2.3fr 1fr; gap: 24px; }
        @media (max-width: 1024px) { .dashboard-grid { grid-template-columns: 1fr; } }

        .card { background: var(--card-bg); border: 1px solid var(--border-color); border-radius: 16px; padding: 20px; backdrop-filter: blur(16px); box-shadow: 0 8px 32px 0 rgba(0, 0, 0, 0.37); margin-bottom: 20px; }
        .card-title { font-size: 15px; font-weight: 600; margin-bottom: 16px; display: flex; align-items: center; justify-content: space-between; }

        /* دکمه رفرش دستی */
        .btn-refresh { background: rgba(59, 130, 246, 0.15); color: var(--primary); border: 1px solid rgba(59, 130, 246, 0.3); padding: 6px 12px; border-radius: 8px; cursor: pointer; font-size: 12px; font-weight: 600; display: inline-flex; align-items: center; gap: 6px; transition: 0.2s; }
        .btn-refresh:hover { background: rgba(59, 130, 246, 0.25); }
        .spin { animation: rotate 0.8s linear infinite; }
        @keyframes rotate { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

        .table-wrapper { overflow-x: auto; }
        table { width: 100%; border-collapse: collapse; text-align: right; }
        th { color: var(--text-muted); font-size: 12px; font-weight: 500; padding: 12px 8px; border-bottom: 1px solid var(--border-color); }
        td { padding: 14px 8px; border-bottom: 1px solid var(--border-color); font-size: 13px; vertical-align: middle; }
        
        .progress-box { width: 100%; max-width: 120px; }
        .progress-bg { height: 6px; background: rgba(255,255,255,0.1); border-radius: 6px; overflow: hidden; margin-top: 4px; }
        .progress-bar { height: 100%; border-radius: 6px; transition: width 0.3s; }

        .tag-status { padding: 3px 8px; border-radius: 6px; font-size: 11px; font-weight: 600; }
        .status-active { background: rgba(16, 185, 129, 0.15); color: var(--success); }
        .status-locked { background: rgba(239, 68, 68, 0.15); color: var(--danger); }

        .btn-copy { border: none; padding: 6px 10px; border-radius: 6px; cursor: pointer; font-size: 11px; font-weight: 500; transition: 0.2s; display: inline-flex; align-items: center; color: #fff; margin-left: 3px; margin-bottom: 3px; }
        .btn-npv { background: #6366f1; }
        .btn-npv:hover { background: #4f46e5; }
        .btn-streisand { background: #0284c7; }
        .btn-streisand:hover { background: #0369a1; }

        .form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
        .form-group { margin-bottom: 12px; }
        label { display: block; font-size: 12px; color: var(--text-muted); margin-bottom: 6px; }
        input { width: 100%; padding: 10px 14px; background: rgba(11, 15, 25, 0.8); border: 1px solid var(--border-color); border-radius: 8px; color: #fff; outline: none; transition: 0.2s; font-size: 13px; }
        input:focus { border-color: var(--primary); }

        .btn { width: 100%; padding: 10px; border: none; border-radius: 8px; font-weight: 600; cursor: pointer; transition: opacity 0.2s; font-size: 13px; }
        .btn:hover { opacity: 0.85; }
        .btn-success { background: var(--success); color: #fff; }
        .btn-warning { background: var(--warning); color: #000; }
        .btn-danger { background: var(--danger); color: #fff; }

        #toast { visibility: hidden; min-width: 260px; background-color: #10b981; color: #fff; text-align: center; border-radius: 8px; padding: 12px; position: fixed; z-index: 1000; left: 50%; bottom: 30px; transform: translateX(-50%); font-size: 14px; box-shadow: 0 4px 15px rgba(0,0,0,0.4); }
        #toast.show { visibility: visible; animation: fadein 0.4s, fadeout 0.4s 2.5s; }
        @keyframes fadein { from { bottom: 0; opacity: 0; } to { bottom: 30px; opacity: 1; } }
        @keyframes fadeout { from { bottom: 30px; opacity: 1; } to { bottom: 0; opacity: 0; } }
    </style>
</head>
<body>

<div class="container">
    <header>
        <h1>⚡ پنل مدیریت سرور SSH VPN</h1>
        <div class="header-actions">
            <span class="badge">پورت اتصال: {{.SSHPort}}</span>
            <a href="/logout" class="logout-btn">خروج</a>
        </div>
    </header>

<!-- کارت‌های آماری و وضعیت سرور -->
    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; margin-bottom: 25px;">
        <div class="card" style="margin-bottom:0; padding: 15px; text-align: center;">
            <div style="color: var(--text-muted); font-size: 12px;">کل کاربران</div>
            <div style="font-size: 22px; font-weight: 700; margin-top: 5px;">{{.TotalUsers}}</div>
        </div>
        <div class="card" style="margin-bottom:0; padding: 15px; text-align: center;">
            <div style="color: var(--success); font-size: 12px;">کاربران فعال</div>
            <div style="font-size: 22px; font-weight: 700; margin-top: 5px;">{{.ActiveUsers}}</div>
        </div>
        <div class="card" style="margin-bottom:0; padding: 15px; text-align: center;">
            <div style="color: var(--danger); font-size: 12px;">کاربران مسدود</div>
            <div style="font-size: 22px; font-weight: 700; margin-top: 5px;">{{.LockedUsers}}</div>
        </div>
        <div class="card" style="margin-bottom:0; padding: 15px; text-align: center;">
            <div style="color: var(--primary); font-size: 12px;">مصرف کل سرور</div>
            <div style="font-size: 22px; font-weight: 700; margin-top: 5px;">{{.TotalUsedGB}}</div>
        </div>
        <div class="card" style="margin-bottom:0; padding: 15px; text-align: center;">
            <div style="color: var(--text-muted); font-size: 12px;">وضعیت SSH / CPU / RAM</div>
            <div style="font-size: 13px; font-weight: 600; margin-top: 8px;">
                SSH: {{.SSHStatus}} | CPU: {{.CPUUsage}} | RAM: {{.RAMUsage}}
            </div>
        </div>
    </div>

    <div class="dashboard-grid">
        <div>
            <div class="card">
                <div class="card-title">
                    <span>👥 لیست کاربران و اشتراک‌ها</span>
                    <button class="btn-refresh" onclick="refreshTable()" id="refBtn">
                        <svg id="refIcon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/></svg>
                        <span>بروزرسانی وضعیت</span>
                    </button>
                </div>
                <div class="table-wrapper">
                    <table>
                        <thead>
                            <tr>
                                <th>کاربر</th>
                                <th>مصرف / کل</th>
                                <th>انقضا</th>
                                <th>آنلاین</th>
                                <th>وضعیت</th>
                                <th>کانفیگ‌ها</th>
                            </tr>
                        </thead>
                        <tbody id="userTableBody">
                            {{range .Users}}
                            <tr>
                                <td><strong>{{.Username}}</strong></td>
                                <td>
                                    <div>{{.UsedMB}} / {{.LimitMB}} MB</div>
                                    <div class="progress-box">
                                        <div class="progress-bg">
                                            <div class="progress-bar" style="width: {{.Percent}}%; background: {{if gt .Percent 85}}var(--danger){{else}}var(--success){{end}};"></div>
                                        </div>
                                    </div>
                                </td>
                                <td style="color: var(--text-muted);">{{.ExpireDate}}</td>
                                <td>{{.ActiveConn}} / {{.MaxConn}}</td>
                                <td>
                                    {{if .IsLocked}}
                                        <span class="tag-status status-locked">مسدود</span>
                                    {{else}}
                                        <span class="tag-status status-active">فعال</span>
                                    {{end}}
                                </td>
                                <td>
                                    <button class="btn-copy btn-npv" onclick="copyConfig('{{.NpvLink}}')">📱 NapsternetV</button>
                                    <button class="btn-copy btn-streisand" onclick="copyConfig('{{.StreisandLink}}')">🍎 Streisand</button>
                                </td>
                            </tr>
                            {{end}}
                        </tbody>
                    </table>
                </div>
            </div>
        </div>

        <div>
            <div class="card">
                <div class="card-title">➕ ایجاد کاربر جدید</div>
                <form action="/add" method="POST">
                    <div class="form-group">
                        <label>نام کاربری</label>
                        <input type="text" name="username" required autocomplete="off">
                    </div>
                    <div class="form-group">
                        <label>رمز عبور</label>
                        <input type="password" name="password" required>
                    </div>
                    <div class="form-group">
                        <label>حجم مجاز (مگابایت)</label>
                        <input type="number" name="traffic" placeholder="مثلاً 20000" required>
                    </div>
                    <div class="form-row">
                        <div class="form-group">
                            <label>مدت اعتبار (روز)</label>
                            <input type="number" name="days" value="30" required>
                        </div>
                        <div class="form-group">
                            <label>کاربر همزمان</label>
                            <input type="number" name="maxconn" value="1" min="1" max="10" required>
                        </div>
                    </div>
                    <button class="btn btn-success" type="submit">ایجاد کاربر</button>
                </form>
            </div>

            <div class="card">
                <div class="card-title">🔄 تمدید / تغییر سقف حجم</div>
                <form action="/update" method="POST">
                    <div class="form-group">
                        <label>نام کاربری</label>
                        <input type="text" name="username" required>
                    </div>
                    <div class="form-group">
                        <label>حجم جدید (مگابایت)</label>
                        <input type="number" name="traffic" required>
                    </div>
                    <button class="btn btn-warning" type="submit">اعمال حجم</button>
                </form>
            </div>

            <div class="card">
                <div class="card-title">❌ حذف کامل کاربر</div>
                <form action="/delete" method="POST">
                    <div class="form-group">
                        <label>نام کاربری</label>
                        <input type="text" name="username" required>
                    </div>
                    <button class="btn btn-danger" type="submit">حذف اکانت</button>
                </form>
            </div>
        </div>
    </div>
</div>

<div id="toast">✅ لینک اشتراک کپی شد! در اپلیکیشن Paste کنید.</div>

<script>
    function copyConfig(link) {
        navigator.clipboard.writeText(link).then(() => {
            const toast = document.getElementById("toast");
            toast.className = "show";
            setTimeout(() => { toast.className = toast.className.replace("show", ""); }, 2800);
        });
    }

    // رفرش دستی اطلاعات با زدن دکمه
    async function refreshTable() {
        const icon = document.getElementById('refIcon');
        icon.classList.add('spin');
        try {
            const res = await fetch('/api/users');
            if (!res.ok) return;
            const users = await res.json();
            
            let html = '';
            users.forEach(u => {
                const color = u.percent > 85 ? 'var(--danger)' : 'var(--success)';
                const statusTag = u.is_locked ? '<span class="tag-status status-locked">مسدود</span>' : '<span class="tag-status status-active">فعال</span>';
                
                html += ` + "`" + `
                <tr>
                    <td><strong>${u.username}</strong></td>
                    <td>
                        <div>${u.used_mb} / ${u.limit_mb} MB</div>
                        <div class="progress-box">
                            <div class="progress-bg">
                                <div class="progress-bar" style="width: ${u.percent}%; background: ${color};"></div>
                            </div>
                        </div>
                    </td>
                    <td style="color: var(--text-muted);">${u.expire_date}</td>
                    <td>${u.active_conn} / ${u.max_conn}</td>
                    <td>${statusTag}</td>
                    <td>
                        <button class="btn-copy btn-npv" onclick="copyConfig('${u.npv_link}')">📱 NapsternetV</button>
                        <button class="btn-copy btn-streisand" onclick="copyConfig('${u.streisand_link}')">🍎 Streisand</button>
                    </td>
                </tr>` + "`" + `;
            });
            document.getElementById('userTableBody').innerHTML = html;
        } catch(e) {
        } finally {
            setTimeout(() => icon.classList.remove('spin'), 400);
        }
    }
</script>

</body>
</html>
`

func isAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(SessionValue)) == 1
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		tmpl, _ := template.New("login").Parse(loginTemplate)
		tmpl.Execute(w, nil)
		return
	}

	user := r.FormValue("username")
	pass := r.FormValue("password")

	if user == AdminUser && pass == AdminPassword {
		http.SetCookie(w, &http.Cookie{
			Name:     SessionCookie,
			Value:    SessionValue,
			Path:     "/",
			HttpOnly: true,
			Expires:  time.Now().Add(24 * time.Hour),
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	tmpl, _ := template.New("login").Parse(loginTemplate)
	tmpl.Execute(w, LoginData{Error: "نام کاربری یا رمز عبور اشتباه است."})
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthenticated(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	users := getUsersData()
	totalUsers := len(users)
	activeUsers := 0
	lockedUsers := 0
	totalBytes := 0

	for _, u := range users {
		totalBytes += u.UsedMB * 1024 * 1024
		if u.IsLocked {
			lockedUsers++
		} else {
			activeUsers++
		}
	}
	totalUsedGB := fmt.Sprintf("%.2f GB", float64(totalBytes)/(1024*1024*1024))

// خواندن درست میزان مصرف CPU در لینوکس / کانتینر
	cpuOut, _ := exec.Command("bash", "-c", "awk '{u=$2+$4; t=$2+$4+$5; if (NR==1){u1=u; t1=t;} else print int((u-u1)*(100)/(t-t1))}' <(grep 'cpu ' /proc/stat) <(sleep 0.2; grep 'cpu ' /proc/stat)").Output()
	cpuUsage := strings.TrimSpace(string(cpuOut))
	if cpuUsage == "" || strings.Contains(cpuUsage, "-") {
		cpuUsage = "0%"
	} else {
		cpuUsage = cpuUsage + "%"
	}

	// خواندن دقیق مصرف RAM کانتینر
	ramOut, _ := exec.Command("bash", "-c", "free -m | grep Mem | awk '{printf \"%.1f%%\", $3/$2 * 100}'").Output()
	ramUsage := strings.TrimSpace(string(ramOut))
	if ramUsage == "" {
		ramUsage = "0%"
	}

	// بررسی وضعیت سرویس SSH
	sshCheck, _ := exec.Command("pgrep", "sshd").Output()
	sshStatus := "آنلاین 🟢"
	if len(sshCheck) == 0 {
		sshStatus = "آفلاین 🔴"
	}

	tmpl, _ := template.New("index").Parse(htmlTemplate)
	tmpl.Execute(w, PageData{
		Users:         users,
		SSHPort:       SSHPort,
		DirectHost:    DirectHost,
		TotalUsers:    totalUsers,
		ActiveUsers:   activeUsers,
		LockedUsers:   lockedUsers,
		TotalUsedGB:   totalUsedGB,
		CPUUsage:      cpuUsage,
		RAMUsage:      ramUsage,
		SSHStatus:     sshStatus,
	})
}

func apiUsersHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(getUsersData())
}

func addHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		user := r.FormValue("username")
		pass := r.FormValue("password")
		limit := r.FormValue("traffic")
		days := r.FormValue("days")
		maxconn := r.FormValue("maxconn")
		if days == "" {
			days = "30"
		}
		if maxconn == "" {
			maxconn = "1"
		}

		exec.Command("/usr/local/bin/add_user", user, pass, limit, days, maxconn).Run()
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func updateHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		user := r.FormValue("username")
		limitMB, _ := strconv.Atoi(r.FormValue("traffic"))
		limitBytes := limitMB * 1024 * 1024

		exec.Command("bash", "-c", "echo "+strconv.Itoa(limitBytes)+" > /etc/vpn_users/"+user+".limit").Run()
		exec.Command("bash", "-c", "rm -f /etc/vpn_users/"+user+".locked").Run()
		exec.Command("usermod", "-U", user).Run()
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		user := r.FormValue("username")
		exec.Command("pkill", "-u", user).Run()
		exec.Command("userdel", "-f", user).Run()
		exec.Command("bash", "-c", "rm -f /etc/vpn_users/"+user+".*").Run()
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func main() {
	go startTelegramBot()
	go startBackupScheduler()

	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/logout", logoutHandler)
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/api/users", apiUsersHandler)
	http.HandleFunc("/add", addHandler)
	http.HandleFunc("/update", updateHandler)
	http.HandleFunc("/delete", deleteHandler)

	log.Println("Server running on :8090...")
	log.Fatal(http.ListenAndServe(":8090", nil))
}
