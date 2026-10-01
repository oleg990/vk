#!/usr/bin/env bash
# Установка бота. Трогает только: пользователя realty-bot, /opt/realty-bot, realty-bot.service.
set -euo pipefail
SRC="$(cd "$(dirname "$0")" && pwd)"
DIR=/opt/realty-bot
UNIT=/etc/systemd/system/realty-bot.service

# Проверки: ничего чужого не перезаписываем.
if [ -e "$UNIT" ] && ! grep -q "Олег Маханько" "$UNIT"; then echo "STOP: $UNIT принадлежит другому проекту"; exit 1; fi
if [ -e "$DIR" ] && [ ! -f "$DIR/realty-bot" ] && [ -n "$(ls -A "$DIR")" ]; then echo "STOP: $DIR занят другим проектом"; exit 1; fi
if id realty-bot >/dev/null 2>&1; then
  [ "$(getent passwd realty-bot | cut -d: -f6)" = "$DIR" ] || { echo "STOP: пользователь realty-bot уже есть и не наш"; exit 1; }
else
  useradd --system --home "$DIR" --shell /usr/sbin/nologin realty-bot
fi

mkdir -p "$DIR/data"
install -m 755 "$SRC/realty-bot" "$DIR/realty-bot.new" && mv "$DIR/realty-bot.new" "$DIR/realty-bot"
if [ -f "$SRC/.env" ]; then
  install -m 600 "$SRC/.env" "$DIR/.env" && rm -f "$SRC/.env"
elif [ ! -f "$DIR/.env" ]; then
  cat > "$DIR/.env" <<ENV
VK_TOKEN=
VK_GROUP_ID=241936618
ADMIN_VK_ID=1095490558
DATA_FILE=$DIR/data/bot.json
TZ_NAME=Europe/Moscow
ENV
fi
# env.add — новые/изменённые ключи: KEY=VALUE построчно; дописываем в .env и удаляем файл
if [ -f "$SRC/env.add" ]; then
  while IFS= read -r line; do
    case "$line" in ''|\#*) continue ;; esac
    key="${line%%=*}"
    grep -v "^${key}=" "$DIR/.env" > "$DIR/.env.tmp" || true
    printf '%s\n' "$line" >> "$DIR/.env.tmp"
    mv "$DIR/.env.tmp" "$DIR/.env"
  done < "$SRC/env.add"
  rm -f "$SRC/env.add"
  echo "env.add применён"
fi
chown -R realty-bot:realty-bot "$DIR"
chmod 600 "$DIR/.env"
install -m 644 "$SRC/realty-bot.service" "$UNIT"
systemctl daemon-reload
if grep -q '^VK_TOKEN=.\+' "$DIR/.env"; then
  systemctl enable realty-bot >/dev/null 2>&1
  systemctl restart realty-bot
  sleep 4
  systemctl --no-pager --lines=15 status realty-bot || true
else
  echo "NEED_TOKEN: впиши ключ в $DIR/.env (VK_TOKEN=...) и запусти: systemctl enable --now realty-bot"
fi
