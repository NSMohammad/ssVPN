#!/bin/bash
echo "📊 لیست کاربران و وضعیت سرویس:"
echo "--------------------------------------------------------"
printf "%-12s | %-12s | %-14s | %-10s | %-8s\n" "کاربر" "مصرف / کل" "تاریخ انقضا" "کاربر همزمان" "وضعیت"
echo "--------------------------------------------------------"

for file in /etc/vpn_users/*.limit; do
    [ -e "$file" ] || continue
    USER=$(basename "$file" .limit)

    TOTAL_BYTES=$(cat "/etc/vpn_users/$USER.used" 2>/dev/null || echo "0")
    LIMIT_BYTES=$(cat "$file")
    USED_MB=$((TOTAL_BYTES / 1024 / 1024))
    LIMIT_MB=$((LIMIT_BYTES / 1024 / 1024))

    EXPIRE=$(cat "/etc/vpn_users/$USER.expire" 2>/dev/null || echo "نامحدود")
    MAX_CONN=$(cat "/etc/vpn_users/$USER.maxconn" 2>/dev/null || echo "1")
    ACTIVE_CONN=$(ps -ef | grep "[s]shd-session: $USER \[priv\]" | wc -l)

    printf "%-12s | %-12s | %-14s | %-10s | %-8s\n" \
      "$USER" "$USED_MB/${LIMIT_MB}MB" "$EXPIRE" "$ACTIVE_CONN/$MAX_CONN" "🟢 فعال"
done

for file in /etc/vpn_users/*.locked; do
    [ -e "$file" ] || continue
    USER=$(basename "$file" .locked)
    TOTAL_BYTES=$(cat "/etc/vpn_users/$USER.used" 2>/dev/null || echo "0")
    USED_MB=$((TOTAL_BYTES / 1024 / 1024))
    EXPIRE=$(cat "/etc/vpn_users/$USER.expire" 2>/dev/null || echo "نامحدود")

    printf "%-12s | %-12s | %-14s | %-10s | %-8s\n" \
      "$USER" "${USED_MB}MB" "$EXPIRE" "0" "🔴 مسدود"
done
