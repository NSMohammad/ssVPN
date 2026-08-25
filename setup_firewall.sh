#!/bin/bash
echo "🛡️ Applying network security & anti-abuse rules..."

# ۱. مسدودسازی ارسال مستقیم ایمیل (جلوگیری از بلک‌لیست شدن IP)
iptables -A FORWARD -p tcp -m multiport --dports 25,465,587 -j DROP
iptables -A OUTPUT -p tcp -m multiport --dports 25,465,587 -j DROP

# ۲. مسدودسازی پروتکل‌های آسیب‌پذیر ویندوزی (SMB / NetBIOS)
iptables -A FORWARD -p tcp -m multiport --dports 135,137,138,139,445 -j DROP
iptables -A FORWARD -p udp -m multiport --dports 135,137,138,139,445 -j DROP

# ۳. مسدودسازی پورت‌های عمومی تورنت و بیت‌تورنت
iptables -A FORWARD -p tcp --dport 6881:6889 -j DROP
iptables -A FORWARD -p udp --dport 6881:6889 -j DROP

echo "✅ Firewall security rules loaded."
