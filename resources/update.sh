#!/usr/bin/env bash

set -e

__self=$(cd $(dirname ${BASH_SOURCE[0]}); pwd -P)
__auth=${MAXMIND_API_CREDENTIALS:-@}
__url="https://download.maxmind.com/geoip/databases/GeoLite2-City/download?suffix=tar.gz"
__tarball=$(curl -sILu ${__auth} ${__url} | grep content-disposition | cut -d= -f2 | tr -d '\r')
__release=$(echo ${__tarball/.tar.gz/} | cut -d_ -f2)

echo Fetching ${__tarball}

cd ${__self}
curl -sJLOu ${__auth} ${__url}
tar -x --strip-components=1 --wildcards -f ${__tarball} \*/GeoLite2-City.mmdb
echo ${__release} > GeoLite2-City.mmdb.version
rm -f ${__tarball}
