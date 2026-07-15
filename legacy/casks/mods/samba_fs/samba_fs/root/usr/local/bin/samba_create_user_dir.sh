#!/usr/bin/with-contenv bash

echo "User login $2"
if [ ! -e "$1/$2" ]; then
  echo "Create user home dir $2"
  mkdir -p "$1/$2"
fi

chown "$2:$2" "$1/$2"
chmod -R 700 "$1/$2"
exit 0
