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
  result=`samba-tool group listmembers "$1" | grep "$2"`
  if [[ "$result" == *"$2"* ]]; then
    echo "$2 already in $1"
  else
    echo "Add $2 to group $1"
    echo $( samba-tool group addmembers "$1" "$2" )
  fi
}

# waiting for samba startup
sleep 20

# set 'Domain Users' group gidNumber
if [ -z $(get_group_attr "Domain Users" gidNumber) ]; then
  echo "Set 'Domain Users' gidNumber: $SAMBA_DC_DOMAIN_USERS_GID_NUMBER"
  echo  $(samba-tool group addunixattrs 'Domain Users' $SAMBA_DC_DOMAIN_USERS_GID_NUMBER)
fi

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
  create_ou "OU=People" $SAMBA_DC_BASE_DN "People"
  create_ou "OU=Servers" $SAMBA_DC_BASE_DN "Servers"
  create_ou "OU=Graveyard" $SAMBA_DC_BASE_DN "Graveyard"
  echo "Craete groups, Role & Access"
  create_ou "OU=Role" "OU=Groups,$SAMBA_DC_BASE_DN" "Role"
  create_ou "OU=Access" "OU=Groups,$SAMBA_DC_BASE_DN" "Access"
  APP_BASE="OU=Role,OU=Groups"

  echo "Create Group Admins"
  create_group "$SAMBA_DC_ADMIN_GROUP_NAME" $APP_BASE "The Administrators use by apps"
  add_to_group "Administrators" "$SAMBA_DC_ADMIN_GROUP_NAME"

  echo "Create Group Unix Admins"
  create_group "Unix Admins" $APP_BASE "Unix Admins"
  add_to_group "Administrators" "Unix Admins"

  echo net rpc rights grant "$SAMBA_DC_WORKGROUP\Unix Admins" SeDiskOperatorPrivilege -U "$SAMBA_DC_ADMINISTRATOR_NAME%******"
  net rpc rights grant "$SAMBA_DC_WORKGROUP\Unix Admins" SeDiskOperatorPrivilege -U "$SAMBA_DC_ADMINISTRATOR_NAME%$SAMBA_DC_ADMINISTRATOR_PASSWORD"
  # create_ou "OU=Computer" "OU=Groups,$SAMBA_DC_BASE_DN" "Computer"
fi

# deal with admin
if [ ! -z "$SAMBA_DC_ADMIN_NAME" ]; then
  echo "Deal with admin"
  sAMAccountName=$( get_attribute_dn "distinguishedName=$SAMBA_DC_ADMIN_DN" sAMAccountName )
  if [ "$sAMAccountName" == "$SAMBA_DC_ADMIN_NAME" ]; then
    echo "$SAMBA_DC_ADMIN_NAME user already exist "
  else
    echo "Create $SAMBA_DC_ADMIN_DN"
    echo $(samba-tool user add $SAMBA_DC_ADMIN_NAME $SAMBA_DC_ADMIN_PASSWORD --userou=$SAMBA_DC_BASE_USERS_DN_PREFIX)
    samba-tool user rename $SAMBA_DC_ADMIN_NAME --display-name=$SAMBA_DC_ADMIN_DISPLAY_NAME
  fi
  echo "Reset $SAMBA_DC_ADMIN_DN password"
  echo $(samba-tool user setpassword $SAMBA_DC_ADMIN_NAME --newpassword=$SAMBA_DC_ADMIN_PASSWORD)
  add_to_group "Domain Admins" $SAMBA_DC_ADMIN_NAME
  add_to_group "Schema Admins" $SAMBA_DC_ADMIN_NAME
  add_to_group "Enterprise Admins" $SAMBA_DC_ADMIN_NAME
  add_to_group "Group Policy Creator Owners" $SAMBA_DC_ADMIN_NAME 
  add_to_group "Administrators" $SAMBA_DC_ADMIN_NAME 
  add_to_group "Admins" $SAMBA_DC_ADMIN_NAME 
fi

# samba password rule
echo "Apply default user password rule"
samba-tool domain passwordsettings set --min-pwd-age=0
echo "Set Samba DC user min password age: 0"
samba-tool domain passwordsettings set --max-pwd-age=$SAMBA_DC_USER_MAX_PASS_AGE
echo "Set Samba DC user max password age: $SAMBA_DC_USER_MAX_PASS_AGE"
samba-tool domain passwordsettings set --min-pwd-length=$SAMBA_DC_USER_MIN_PASS_LENGTH
echo "Set Samba DC user min password length: $SAMBA_DC_USER_MIN_PASS_LENGTH"
samba-tool domain passwordsettings set --history-length=0
echo "Set Samba DC user password history length: 0"
if [ $SAMBA_DC_USER_COMPLEX_PASS == "true" ]; then
  samba-tool domain passwordsettings set --complexity=on
else
  samba-tool domain passwordsettings set --complexity=off
fi
echo "Set Samba DC user password complex: $SAMBA_DC_USER_COMPLEX_PASS"

# samba administrator password rule 
#TODO: fix password rule
echo $(samba-tool domain passwordsettings pso create "pso_administrator" 1 --min-pwd-length=7  --complexity=on \
              --history-length=0 --min-pwd-age=0 --max-pwd-age=0) 
admin_pso="$(samba-tool domain passwordsettings pso show-user $SAMBA_DC_ADMINISTRATOR_NAME)"
if [[ "$admin_pso" != *"pso_administrator"* ]]; then
  echo "Apply administrator user password rule"  
  echo $(samba-tool domain passwordsettings pso apply "pso_administrator" $SAMBA_DC_ADMINISTRATOR_NAME)
fi

# change dsheuristics to allow user modify password
samba-tool forest directory_service dsheuristics 000000001

echo "The structure has been set up."
