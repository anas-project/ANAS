#!/usr/bin/with-contenv bash

LDBSEARCH_CMD_PREFIX="ldbsearch -H /var/lib/samba/private/sam.ldb"

get_attribute_dn() { # $1 filter, $2 attritube name
  echo $( $LDBSEARCH_CMD_PREFIX "$1" $2 | grep $2 | sed -nr "s/$2: ([\w|,|=]*)/\1/p" )
}

get_group_attr() { # $1 group cn name, $2 attritube name
  echo $( samba-tool group show "$1" | grep $2 | sed -nr "s/$2: ([\w|,|=]*)/\1/p" )
}

dn_exist() { # $1 dn path 
  search_dn=$( get_attribute_dn "distinguishedName=$1" dn )
  [ "$search_dn" == "$1" ]
}

create_ou() { # $1 ou name, $2 base dn $3 description
  dn="$1,$2"
  if dn_exist "$dn"; then
    echo "dn: $dn is exist"
  else
    echo $( samba-tool ou create "$dn" --description="$3" )
    echo "Create dn: $dn description: $3"
  fi
}

create_group() { # $1 group name, $2 base dn $3 description
  dn="CN=$1,$2,$SAMBA_DC_BASE_DN"
  if dn_exist "$dn"; then
    echo "dn: $dn is exist"
  else
    echo $( samba-tool group add "$1" --groupou="$2" --description="$3" )
    echo "Create dn:$dn description: '$3'"
  fi
}

add_to_group() { # $1 group name, $2 object name
  result=$(samba-tool group listmembers "$1" | grep -Fx "$2" || true)
  if [[ "$result" == *"$2"* ]]; then
    echo "$2 already in $1"
  else
    echo "Add $2 to group $1"
    echo $( samba-tool group addmembers "$1" "$2" )
  fi
}

remove_from_group() { # $1 group name, $2 object name
  result=$(samba-tool group listmembers "$1" | grep -Fx "$2" || true)
  if [ -n "$result" ]; then
    echo "Remove $2 from $1"
    samba-tool group removemembers "$1" "$2"
  fi
}

# waiting for samba startup
sleep 20

# A service account is always required by LDAP-integrated applications, even
# when the optional organizational structure is disabled.
create_ou "OU=People" "$SAMBA_DC_BASE_DN" "People"
create_ou "OU=Admins" "$SAMBA_DC_BASE_DN" "Privileged user accounts"
create_ou "OU=Service Accounts" "$SAMBA_DC_BASE_DN" "Non-interactive service accounts"

# app filter by group
if [ $SAMBA_DC_APP_FILTER == "true" ]; then
  echo "Create app filter ou & group"
  create_ou "OU=Groups" $SAMBA_DC_BASE_DN "Groups"
  create_ou "OU=Apps" "OU=Groups,$SAMBA_DC_BASE_DN" "Apps"
  APP_BASE="OU=Apps,OU=Groups"
  # User can access all app if add to this group
  create_group "$SAMBA_DC_APP_ALL_NAME" "$APP_BASE" "Add user to this group, can access all app when \$SAMBA_DC_APP_FILTER == true"
  for name in $(echo $USE_LDAP_MODS_NAME | tr "," "\n")
  do
    create_group "APP_$name" $APP_BASE "Add user to this group, can access app $name"
    # Add $SAMBA_DC_APP_ALL_NAME group to APP_$name, for recursive the permission
    add_to_group "APP_$name" "$SAMBA_DC_APP_ALL_NAME"
  done
fi

# auto create ldap structure
if [ $SAMBA_DC_CREATE_STRUCTURE == "true" ]; then
  echo "Create basic structure ou & group"
  create_ou "OU=Groups" $SAMBA_DC_BASE_DN "Groups"
  create_ou "OU=Servers" $SAMBA_DC_BASE_DN "Servers"
  create_ou "OU=Graveyard" $SAMBA_DC_BASE_DN "Graveyard"
  echo "Craete groups, Role & Access"
  create_ou "OU=Role" "OU=Groups,$SAMBA_DC_BASE_DN" "Role"
  create_ou "OU=Access" "OU=Groups,$SAMBA_DC_BASE_DN" "Access"
  ROLE_BASE="OU=Role,OU=Groups"
  ACCESS_BASE="OU=Access,OU=Groups"

  echo "Create Group Admins"
  create_group "$SAMBA_DC_ADMIN_GROUP_NAME" "$ROLE_BASE" "Application administrator role; does not grant domain administration"
  remove_from_group "Administrators" "$SAMBA_DC_ADMIN_GROUP_NAME"

  echo "Create Group Unix Admins"
  create_group "Unix Admins" "$ROLE_BASE" "Domain infrastructure administrators"
  add_to_group "Administrators" "Unix Admins"

  echo net rpc rights grant "$SAMBA_DC_WORKGROUP\Unix Admins" SeDiskOperatorPrivilege -U "$SAMBA_DC_ADMINISTRATOR_NAME%******"
  net rpc rights grant "$SAMBA_DC_WORKGROUP\Unix Admins" SeDiskOperatorPrivilege -U "$SAMBA_DC_ADMINISTRATOR_NAME%$SAMBA_DC_ADMINISTRATOR_PASSWORD"

  echo "Create file service resource groups"
  create_group "$SAMBA_DC_FS_ADMIN_GROUP_NAME" "$ACCESS_BASE" "Administrators with root-equivalent access on Samba FS"
  create_group "$SAMBA_DC_FS_SHARE_RW_GROUP_NAME" "$ACCESS_BASE" "Read and write access to the shared data directory"
  # create_ou "OU=Computer" "OU=Groups,$SAMBA_DC_BASE_DN" "Computer"
fi

# deal with admin
if [ ! -z "$SAMBA_DC_ADMIN_NAME" ]; then
  echo "Deal with admin"
  # Located by account name rather than by DN: a deployment provisioned before
  # the account moved into OU=People still has it somewhere else, and that has
  # to be recognised as the same account instead of a missing one.
  admin_dn=$( get_attribute_dn "sAMAccountName=$SAMBA_DC_ADMIN_NAME" dn )
  if [ -z "$admin_dn" ]; then
    echo "Create $SAMBA_DC_ADMIN_DN"
    samba-tool user add "$SAMBA_DC_ADMIN_NAME" "$SAMBA_DC_ADMIN_PASSWORD" \
      --userou="$SAMBA_DC_BASE_USERS_DN_PREFIX"
    samba-tool user rename $SAMBA_DC_ADMIN_NAME --display-name=$SAMBA_DC_ADMIN_DISPLAY_NAME
  elif [ "$admin_dn" != "$SAMBA_DC_ADMIN_DN" ]; then
    echo "Move $admin_dn to $SAMBA_DC_BASE_USERS_DN"
    samba-tool user move "$SAMBA_DC_ADMIN_NAME" "$SAMBA_DC_BASE_USERS_DN"
  else
    echo "$SAMBA_DC_ADMIN_NAME user already exist "
  fi
  add_to_group "Domain Admins" $SAMBA_DC_ADMIN_NAME
  add_to_group "Group Policy Creator Owners" $SAMBA_DC_ADMIN_NAME 
  add_to_group "Administrators" $SAMBA_DC_ADMIN_NAME 
  add_to_group "Admins" $SAMBA_DC_ADMIN_NAME 
  if [ "$SAMBA_DC_CREATE_STRUCTURE" == "true" ]; then
    add_to_group "$SAMBA_DC_FS_ADMIN_GROUP_NAME" "$SAMBA_DC_ADMIN_NAME"
  fi
  if [ "$SAMBA_DC_APP_FILTER" == "true" ]; then
    add_to_group "$SAMBA_DC_APP_ALL_NAME" "$SAMBA_DC_ADMIN_NAME"
    for name in $(echo "$USE_LDAP_MODS_NAME" | tr "," "\n"); do
      add_to_group "APP_$name" "$SAMBA_DC_ADMIN_NAME"
    done
  fi
  remove_from_group "Schema Admins" $SAMBA_DC_ADMIN_NAME
  remove_from_group "Enterprise Admins" $SAMBA_DC_ADMIN_NAME
fi

# Applications use a normal directory account for LDAP search. Domain users can
# read the attributes consumed by the bundled applications; this account is not
# added to any administrative group.
if ! samba-tool user show "$SAMBA_DC_LDAP_BIND_NAME" >/dev/null 2>&1; then
  echo "Create LDAP bind service account $SAMBA_DC_LDAP_BIND_NAME"
  samba-tool user add "$SAMBA_DC_LDAP_BIND_NAME" "$SAMBA_DC_LDAP_BIND_PASSWORD" \
    --userou="OU=Service Accounts"
fi
samba-tool user setexpiry "$SAMBA_DC_LDAP_BIND_NAME" --noexpiry

# Password-capable applications share a dedicated account with only the
# inherited Reset Password extended right on ordinary users in OU=People. It
# cannot create/delete users or manage privileged and service-account OUs.
if ! samba-tool user show "$SAMBA_DC_PASSWORD_BIND_NAME" >/dev/null 2>&1; then
  echo "Create password service account $SAMBA_DC_PASSWORD_BIND_NAME"
  samba-tool user add "$SAMBA_DC_PASSWORD_BIND_NAME" "$SAMBA_DC_PASSWORD_BIND_PASSWORD" \
    --userou="OU=Service Accounts"
fi
samba-tool user setexpiry "$SAMBA_DC_PASSWORD_BIND_NAME" --noexpiry
password_bind_sid=$(samba-tool user show "$SAMBA_DC_PASSWORD_BIND_NAME" | sed -n 's/^objectSid: //p')
reset_password_guid="00299570-246d-11d0-a768-00aa006e0529"
reset_password_ace="(OA;CI;CR;$reset_password_guid;;$password_bind_sid)"
if ! samba-tool dsacl get --objectdn="$SAMBA_DC_BASE_USERS_DN" | grep -Fq "$reset_password_ace"; then
  samba-tool dsacl set --objectdn="$SAMBA_DC_BASE_USERS_DN" --sddl="$reset_password_ace"
fi

# The right above is inherited by everything in OU=People, and the admin account
# now lives there too. Without this the password service account could reset the
# domain administrator's password, and its credentials sit in the configuration
# of every application that offers a password-change form. An explicit ACE on
# the object wins over the inherited one, so the admin is carved back out.
if [ ! -z "$SAMBA_DC_ADMIN_NAME" ]; then
  deny_reset_ace="(OD;;CR;$reset_password_guid;;$password_bind_sid)"
  if ! samba-tool dsacl get --objectdn="$SAMBA_DC_ADMIN_DN" | grep -Fq "$deny_reset_ace"; then
    echo "Deny $SAMBA_DC_PASSWORD_BIND_NAME the Reset Password right on $SAMBA_DC_ADMIN_NAME"
    samba-tool dsacl set --objectdn="$SAMBA_DC_ADMIN_DN" --sddl="$deny_reset_ace"
  fi
fi

# samba password rule
echo "Apply default user password rule"
echo "Set Samba DC user min password age: $SAMBA_DC_USER_MIN_PASS_AGE"
samba-tool domain passwordsettings set --min-pwd-age=$SAMBA_DC_USER_MIN_PASS_AGE
samba-tool domain passwordsettings set --max-pwd-age=$SAMBA_DC_USER_MAX_PASS_AGE
echo "Set Samba DC user max password age: $SAMBA_DC_USER_MAX_PASS_AGE"
samba-tool domain passwordsettings set --min-pwd-length=$SAMBA_DC_USER_MIN_PASS_LENGTH
echo "Set Samba DC user min password length: $SAMBA_DC_USER_MIN_PASS_LENGTH"
samba-tool domain passwordsettings set --history-length=$SAMBA_DC_USER_PASSWORD_HISTORY
echo "Set Samba DC user password history length: $SAMBA_DC_USER_PASSWORD_HISTORY"
samba-tool domain passwordsettings set --account-lockout-threshold=$SAMBA_DC_USER_LOCKOUT_THRESHOLD
samba-tool domain passwordsettings set --account-lockout-duration=$SAMBA_DC_USER_LOCKOUT_DURATION
samba-tool domain passwordsettings set --reset-account-lockout-after=$SAMBA_DC_USER_LOCKOUT_RESET_AFTER
if [ $SAMBA_DC_USER_COMPLEX_PASS == "true" ]; then
  samba-tool domain passwordsettings set --complexity=on
else
  samba-tool domain passwordsettings set --complexity=off
fi
echo "Set Samba DC user password complex: $SAMBA_DC_USER_COMPLEX_PASS"

# Separate policy object for privileged accounts.
if samba-tool domain passwordsettings pso list | grep -Fxq "pso_privileged"; then
  samba-tool domain passwordsettings pso set "pso_privileged" --min-pwd-length=8 --complexity=on \
    --history-length=4 --min-pwd-age=1 --max-pwd-age=60
else
  samba-tool domain passwordsettings pso create "pso_privileged" 1 --min-pwd-length=8 --complexity=on \
    --history-length=4 --min-pwd-age=1 --max-pwd-age=60
fi
samba-tool domain passwordsettings pso apply "pso_privileged" "$SAMBA_DC_ADMINISTRATOR_NAME" || true
samba-tool domain passwordsettings pso apply "pso_privileged" "$SAMBA_DC_ADMIN_GROUP_NAME" || true

# change dsheuristics to allow user modify password
samba-tool forest directory_service dsheuristics 000000001

echo "The structure has been set up."
