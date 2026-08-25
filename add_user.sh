#!/bin/bash
if [ "$#" -ne 5 ]; then
    echo "Usage: add_user <username> <password> <limit_in_MB> <days_valid> <max_connections>"
    exit 1
fi

USER=$1
PASS=$2
LIMIT=$3
DAYS=$4
MAX_CONN=$5

# محاسبه تاریخ انقضا
EXPIRE_DATE=$(date -d "+$DAYS days" +%Y-%m-%d)

# ساخت کاربر لینوکس با تاریخ انقضا و بدون شل
useradd -M -s /sbin/nologin -e "$EXPIRE_DATE" "$USER" 2>/dev/null || echo "User already exists."
echo "$USER:$PASS" | chpasswd
UID_NUM=$(id -u "$USER")

# رول‌های بازگردانی مارک
iptables -t mangle -C PREROUTING -j CONNMARK --restore-mark 2>/dev/null || \
iptables -t mangle -I PREROUTING 1 -j CONNMARK --restore-mark

iptables -t mangle -C OUTPUT -j CONNMARK --restore-mark 2>/dev/null || \
iptables -t mangle -I OUTPUT 1 -j CONNMARK --restore-mark

# مارک کردن و شمارنده‌ها
iptables -t mangle -A OUTPUT -m owner --uid-owner "$UID_NUM" -j CONNMARK --set-mark "$UID_NUM" 2>/dev/null
iptables -t mangle -A INPUT -m connmark --mark "$UID_NUM" -m comment --comment "DL_$USER" 2>/dev/null
iptables -t mangle -A OUTPUT -m connmark --mark "$UID_NUM" -m comment --comment "UL_$USER" 2>/dev/null

# ذخیره اطلاعات متادیتا
LIMIT_BYTES=$((LIMIT * 1024 * 1024))
echo "$LIMIT_BYTES" > /etc/vpn_users/"$USER".limit
echo "$PASS" > /etc/vpn_users/"$USER".auth
echo "$EXPIRE_DATE" > /etc/vpn_users/"$USER".expire
echo "$MAX_CONN" > /etc/vpn_users/"$USER".maxconn
[ ! -f "/etc/vpn_users/$USER.used" ] && echo "0" > /etc/vpn_users/"$USER".used

echo "✅ User '$USER' created with $DAYS days validity and $MAX_CONN max connections."
