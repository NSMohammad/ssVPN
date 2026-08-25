#!/bin/bash
set -e

# تولید کلیدهای SSH
ssh-keygen -A

# اعمال قوانین فایروال
/usr/local/bin/setup_firewall.sh

# اجرای سرویس SSH
/usr/sbin/sshd

# اجرای مانیتورینگ
/usr/local/bin/monitor &

# اجرای پنل وب
exec /usr/local/bin/web_panel
