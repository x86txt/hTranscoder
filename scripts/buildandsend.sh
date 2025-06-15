#!/bin/bash
cd /home/matt/hTranscode
scripts/build.sh && \
scp build/linux-amd64/worker matt@plex.ip.lan:/home/matt/ && \
scp build/linux-amd64/api matt@plex.ip.lan:/home/matt/ && \
scp build/linux-amd64/worker matt@pve3.ip.lan:/home/matt/ && \
scp build/linux-amd64/worker matt@pve4.ip.lan:/home/matt/

