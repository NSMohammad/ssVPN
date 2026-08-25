#!/bin/bash

# مشخصات ربات تلگرام برای ارسال اعلان مسدودسازی (در صورت تمایل پر کنید)
TG_TOKEN=""
TG_ADMIN=""

send_tg_alert() {
    local text="$1"
    if [ -n "$TG_TOKEN" ] && [ -n "$TG_ADMIN" ]; then
        curl -s -X POST "https://api.telegram.org/bot${TG_TOKEN}/sendMessage" \
            -d "chat_id=${TG_ADMIN}" \
            -d "text=${text}" \
            -d "parse_mode=HTML" >/dev/null 2>&1
    fi
}

echo "🚀 Initializing firewall tracking rules..."

iptables -t mangle -C PREROUTING -j CONNMARK --restore-mark 2>/dev/null || \
iptables -t mangle -I PREROUTING 1 -j CONNMARK --restore-mark

iptables -t mangle -C OUTPUT -j CONNMARK --restore-mark 2>/dev/null || \
iptables -t mangle -I OUTPUT 1 -j CONNMARK --restore-mark

echo "🚀 Restoring users from persistent volume..."
for auth_file in /etc/vpn_users/*.auth; do
    [ -e "$auth_file" ] || continue
    USER=$(basename "$auth_file" .auth)
    PASS=$(cat "$auth_file")
    EXPIRE_DATE=$(cat "/etc/vpn_users/$USER.expire" 2>/dev/null || echo "")

    # ساخت کاربر در سیستم عامل در صورت عدم وجود
    if ! id -u "$USER" &>/dev/null; then
        if [ -n "$EXPIRE_DATE" ] && [ "$EXPIRE_DATE" != "نامحدود" ]; then
            useradd -M -s /sbin/nologin -e "$EXPIRE_DATE" "$USER"
        else
            useradd -M -s /sbin/nologin "$USER"
        fi
        echo "$USER:$PASS" | chpasswd
        echo "♻️ Restored user: $USER"
    fi

    [ ! -f "/etc/vpn_users/$USER.used" ] && echo "0" > "/etc/vpn_users/$USER.used"
    echo "0" > "/tmp/${USER}_last_iptables"

    UID_NUM=$(id -u "$USER" 2>/dev/null)
    if [ -n "$UID_NUM" ]; then
        iptables -t mangle -C OUTPUT -m owner --uid-owner "$UID_NUM" -j CONNMARK --set-mark "$UID_NUM" 2>/dev/null || \
        iptables -t mangle -A OUTPUT -m owner --uid-owner "$UID_NUM" -j CONNMARK --set-mark "$UID_NUM"

        iptables -t mangle -C INPUT -m connmark --mark "$UID_NUM" -m comment --comment "DL_$USER" 2>/dev/null || \
        iptables -t mangle -A INPUT -m connmark --mark "$UID_NUM" -m comment --comment "DL_$USER"

        iptables -t mangle -C OUTPUT -m connmark --mark "$UID_NUM" -m comment --comment "UL_$USER" 2>/dev/null || \
        iptables -t mangle -A OUTPUT -m connmark --mark "$UID_NUM" -m comment --comment "UL_$USER"
    fi

    if [ -f "/etc/vpn_users/$USER.locked" ]; then
        usermod -L "$USER" 2>/dev/null
    fi
done

echo "🚀 Live Monitor (Traffic + Multi-Login + Expiry) Started..."

while true; do
    TODAY=$(date +%Y-%m-%d)

    for file in /etc/vpn_users/*.limit; do
        [ -e "$file" ] || continue
        USER=$(basename "$file" .limit)
        LIMIT_BYTES=$(cat "$file")
        MAX_CONN=$(cat "/etc/vpn_users/$USER.maxconn" 2>/dev/null || echo "1")
        EXPIRE_DATE=$(cat "/etc/vpn_users/$USER.expire" 2>/dev/null || echo "")

        # ۱. بررسی انقضای زمانی
        if [ -n "$EXPIRE_DATE" ] && [ "$EXPIRE_DATE" != "نامحدود" ] && [[ "$TODAY" > "$EXPIRE_DATE" ]]; then
            echo "❌ User $USER expired on $EXPIRE_DATE. Locking..."
            usermod -L "$USER"
            pkill -u "$USER"
            mv "$file" "/etc/vpn_users/$USER.locked"
            send_tg_alert "⏳ <b>اعلان انقضای اکانت</b>%0A👤 کاربر: <code>$USER</code>%0A📅 تاریخ انقضا: $EXPIRE_DATE"
            continue
        fi

        # ۲. بررسی و مدیریت اتصال همزمان
        ONLINE_COUNT=$(ps -ef | grep "[s]shd-session: $USER \[priv\]" | wc -l)
        if [ "$ONLINE_COUNT" -gt "$MAX_CONN" ]; then
            echo "⚠️ User $USER exceeded max connections ($ONLINE_COUNT > $MAX_CONN). Terminating excess session..."
            EXTRA_PID=$(ps -ef | grep "[s]shd-session: $USER \[priv\]" | tail -n 1 | awk '{print $1}')
            if [ -n "$EXTRA_PID" ]; then
                kill -9 "$EXTRA_PID"
            fi
        fi

        # ۳. محاسبه ترافیک تجمعی
        DL=$(iptables -t mangle -L INPUT -v -n -x | grep "DL_$USER" | awk '{s+=$2} END {print s}')
        UL=$(iptables -t mangle -L OUTPUT -v -n -x | grep "UL_$USER" | awk '{s+=$2} END {print s}')
        RAW_IPTABLES=$(( ${DL:-0} + ${UL:-0} ))

        LAST_IPTABLES=$(cat "/tmp/${USER}_last_iptables" 2>/dev/null || echo "0")
        SAVED_USED=$(cat "/etc/vpn_users/$USER.used" 2>/dev/null || echo "0")

        if [ "$RAW_IPTABLES" -ge "$LAST_IPTABLES" ]; then
            DELTA=$((RAW_IPTABLES - LAST_IPTABLES))
        else
            DELTA=$RAW_IPTABLES
        fi

        TOTAL_USED=$((SAVED_USED + DELTA))
        echo "$TOTAL_USED" > "/etc/vpn_users/$USER.used"
        echo "$RAW_IPTABLES" > "/tmp/${USER}_last_iptables"

        # ۴. بررسی سقف حجم و مسدودسازی
        if [ "$TOTAL_USED" -ge "$LIMIT_BYTES" ]; then
            USED_MB=$((TOTAL_USED / 1024 / 1024))
            LIMIT_MB=$((LIMIT_BYTES / 1024 / 1024))
            echo "❌ User $USER exceeded limit ($USED_MB / $LIMIT_MB MB). Locking..."
            usermod -L "$USER"
            pkill -u "$USER"
            mv "$file" "/etc/vpn_users/$USER.locked"
            send_tg_alert "🚨 <b>اعلان اتمام حجم</b>%0A👤 کاربر: <code>$USER</code>%0A💾 مصرف: ${USED_MB}MB از ${LIMIT_MB}MB"
        fi
    done
    sleep 10
done
