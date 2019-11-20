# controller-psi-light
control software for the PSI fytopanel lights



## usage
### options
```
--no-metrics: dont collect or send metrics to telegraf
--dummy: dont control the lights, only collect metrics
--conditions: conditions to use to run the lights
--scroll-mode: scroll all the channels sequentially
--disco-mode: randomly set channel values
--interval: what interval to run conditions/record metrics at, set to 0s to read 1 metric and exit. (default=10m)
--absolute: if your file is set in absolute light values (0-1022) rather than brightness percentages, set this flag
--host-tag: adds a host tag sent to telegraf
--group-tag: adds a group tag sent to telegraf
```


examples
./controller-psi-light --conditions conditions/testfile.csv /dev/ttyUSB0

./controller-psi-light --no-metrics --conditions conditions/testfile.csv /dev/ttyUSB0

./controller-psi-light --disco-mode /dev/ttyUSB0

./controller-psi-light --scroll-mode /dev/ttyUSB0

### environment variables
CONDITIONS_FILE
NO_METRICS
DUMMY
ABSOLUTE
INTEVAL
NAME
HOST_TAG
DISCO
SCROLL

### docker
use the docker image by pulling from the appf repo

`docker pull appf/controller-psi-light`

create a docker container:

```docker create --name lights-conditions \
  -e TZ=Australia/Brisbane \
  -v /home/stormaes/conditions:/conditions \
  --network services-net \
appf/controller-psi-light
```

if you have a docker telegraf instance, it is helpful to add them to the same network, otherwise you can use the
environment variable "TELEGRAF_HOST" to specify the available telegraf host.

start the docker container

`docker start lights-conditions`


## RPi controller

There is now a raspberry pi controller. use the hypriotos flash script to flash a card.

Remember to set the time in your rtc and also uncomment the correct rtc type in the config.txt

`sudo ./flash -u user-data.yml --bootconf config.txt -d /dev/sdx https://github.com/hypriot/image-builder-rpi/releases/download/v1.11.4/hypriotos-rpi-v1.11.4.img.zip`
