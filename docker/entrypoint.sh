#!/usr/bin/env sh

echo "Entrypoint params are: $*"

# Set proper host and container names if docker socket is mounted
if [ -S /var/run/docker.sock ]; then
  # shellcheck disable=SC2039
  old_hostname="${HOSTNAME}"
  container_name=$(curl -s -XGET --unix-socket /var/run/docker.sock -H "Content-Type: application/json" "http://v1.42/containers/${old_hostname=}/json" | jq -r .Name[1:])
  host_name=${container_name#*-}
  echo "${host_name}" > /etc/hostname
  hostname -F /etc/hostname
  echo "Host name is set to: ${host_name}"

  # https://docs.docker.com/engine/api/v1.42/#tag/Container/operation/ContainerRename
  curl --fail -s -XPOST --unix-socket /var/run/docker.sock -H "Content-Type: application/json" "http://v1.42/containers/${container_name}/rename?name=${host_name}"
  echo "Container name is set to: ${host_name}"
fi;

# Chown local binaries
chown user:user -R /usr/local/bin/

/sbin/su-exec user:user "$@"

## shellcheck disable=SC2181
#if [ "$?" -ne 0 ]; then
#  tail -f /dev/null
#fi
