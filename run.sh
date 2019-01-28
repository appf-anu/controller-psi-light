#!/bin/ash
if [ -n "$(find /conditions -maxdepth 0 -type f 2>/dev/null)" ]; then
    controller-psi-light $ARGS /dev/ttyUSB0
else
    COND_F=`ls /conditions/ | head -1`
    controller-psi-light $ARGS --conditions "/conditions/$COND_F" /dev/ttyUSB0
fi


