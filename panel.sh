#!/bin/bash

CONTAINER="simple_ssh_vpn"

function show_menu() {
    clear
    echo "=================================================="
    echo "            🚀 پنل مدیریت SSH VPN 🚀            "
    echo "=================================================="
    echo "  1) ➕ ساخت کاربر جدید"
    echo "  2) 📊 مشاهده ترافیک و کاربران"
    echo "  3) 🔄 تمدید حجم یا خروج از مسدودی"
    echo "  4) ❌ حذف کامل کاربر"
    echo "  0) 🚪 خروج"
    echo "=================================================="
    read -p "انتخاب شما (0-4): " choice
    handle_choice "$choice"
}

function handle_choice() {
    echo ""
    case $1 in
        1) add_user ;;
        2) show_traffic ;;
        3) edit_user ;;
        4) delete_user ;;
        0) echo "خداحافظ! 👋"; exit 0 ;;
        *) echo "❌ انتخاب نامعتبر!"; pause ;;
    esac
}

function add_user() {
    read -p "نام کاربری: " user
    read -p "رمز عبور: " pass
    read -p "حجم (به مگابایت): " limit
    
    echo "در حال ساخت کاربر..."
    docker exec -it $CONTAINER add_user "$user" "$pass" "$limit"
    pause
}

function show_traffic() {
    docker exec -it $CONTAINER bash -c '
    echo "📊 وضعیت مصرف (دانلود + آپلود):"
    echo "-----------------------------------"
    for file in /etc/vpn_users/*.limit; do
        [ -e "$file" ] || continue
        USER=$(basename "$file" .limit)
        
        DL=$(iptables -t mangle -L INPUT -v -n -x | grep "DL_$USER" | awk "{s+=\$2} END {print s}")
        UL=$(iptables -t mangle -L OUTPUT -v -n -x | grep "UL_$USER" | awk "{s+=\$2} END {print s}")
        
        DL=${DL:-0}
        UL=${UL:-0}
        TOTAL_BYTES=$((DL + UL))
        LIMIT_BYTES=$(cat "$file")
        
        USED_MB=$((TOTAL_BYTES / 1024 / 1024))
        LIMIT_MB=$((LIMIT_BYTES / 1024 / 1024))
        
        echo "🟢 $USER | مصرف: $USED_MB مگابایت از $LIMIT_MB مگابایت"
    done
    
    echo ""
    echo "❌ مسدود شده‌ها:"
    echo "-----------------------------------"
    for file in /etc/vpn_users/*.locked; do
        [ -e "$file" ] || continue
        echo "🚫 $(basename "$file" .locked)"
    done
    '
    pause
}

function edit_user() {
    read -p "نام کاربری برای تمدید/تغییر حجم: " user
    read -p "حجم جدید (به مگابایت): " limit
    
    echo "در حال اعمال تغییرات..."
    docker exec -it $CONTAINER bash -c "
        LIMIT_BYTES=\$(( ${limit} * 1024 * 1024 ))
        
        # اگر کاربر مسدود بود، او را آزاد کن
        if [ -f /etc/vpn_users/${user}.locked ]; then
            mv /etc/vpn_users/${user}.locked /etc/vpn_users/${user}.limit
            usermod -U ${user} 2>/dev/null
            echo '🔓 اکانت کاربر از مسدودی خارج شد.'
        fi
        
        # آپدیت فایل حجم
        if [ -f /etc/vpn_users/${user}.limit ]; then
            echo \$LIMIT_BYTES > /etc/vpn_users/${user}.limit
            
            # پاک کردن رول‌های قبلی برای صفر شدن کانتر ترافیک
            UID_NUM=\$(id -u ${user} 2>/dev/null)
            if [ -n \"\$UID_NUM\" ]; then
                iptables -t mangle -D OUTPUT -m owner --uid-owner \$UID_NUM -j CONNMARK --set-mark \$UID_NUM 2>/dev/null
                iptables -t mangle -D INPUT -m connmark --mark \$UID_NUM -m comment --comment \"DL_${user}\" 2>/dev/null
                iptables -t mangle -D OUTPUT -m connmark --mark \$UID_NUM -m comment --comment \"UL_${user}\" 2>/dev/null
            fi
            
            echo '✅ حجم آپدیت شد و مصرف کاربر صفر شد! (رول‌ها در یک دقیقه آینده خودکار فعال می‌شوند)'
        else
            echo '❌ کاربر پیدا نشد.'
        fi
    "
    pause
}

function delete_user() {
    read -p "نام کاربری برای حذف کامل: " user
    
    echo "در حال حذف..."
    docker exec -it $CONTAINER bash -c "
        UID_NUM=\$(id -u ${user} 2>/dev/null)
        if [ -n \"\$UID_NUM\" ]; then
            # کشتن اتصال‌های فعال
            pkill -u ${user} 2>/dev/null
            
            # پاک کردن رول‌های iptables
            iptables -t mangle -D OUTPUT -m owner --uid-owner \$UID_NUM -j CONNMARK --set-mark \$UID_NUM 2>/dev/null
            iptables -t mangle -D INPUT -m connmark --mark \$UID_NUM -m comment --comment \"DL_${user}\" 2>/dev/null
            iptables -t mangle -D OUTPUT -m connmark --mark \$UID_NUM -m comment --comment \"UL_${user}\" 2>/dev/null
            
            # حذف فایل‌ها و اکانت
            rm -f /etc/vpn_users/${user}.*
            userdel -r ${user} 2>/dev/null
            
            echo '🗑️ کاربر و تمام اطلاعاتش با موفقیت حذف شد.'
        else
            echo '❌ کاربر در سیستم یافت نشد.'
        fi
    "
    pause
}

function pause() {
    echo ""
    read -p "برای بازگشت به منو دکمه Enter را بزنید..."
    show_menu
}

# شروع برنامه
show_menu
