#!/usr/bin/with-contenv bash

sleep 10

echo "Fixing /$USERDATA_NAME/$SHARE_DIR_NAME..."
chown root:'Domain Users' /$USERDATA_NAME/$SHARE_DIR_NAME
chmod 0770 /$USERDATA_NAME/$SHARE_DIR_NAME
