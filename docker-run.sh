#!/bin/sh

if [[ -z "$GID" ]]; then
	GID="$UID"
fi

# # Define functions.
function fixperms {
	chown -R $UID:$GID /data /opt/matrix-pulsesms
}

if [[ ! -f /data/config.yaml ]]; then
	cp /opt/matrix-pulsesms/example-config.yaml /data/config.yaml
	echo "Didn't find a config file."
	echo "Copied default config file to /data/config.yaml"
	echo "Modify that config file to your liking."
	echo "Start the container again after that to generate the registration file."
	exit
fi

if [[ ! -f /data/registration.yaml ]]; then
	/usr/bin/matrix-pulsesms -g -c /data/config.yaml -r /data/registration.yaml
	echo "Didn't find a registration file."
	echo "Generated one for you."
	echo "Copy that over to synapses app service directory."
	exit
fi

# cd /data
fixperms
exec su-exec $UID:$GID /usr/bin/matrix-pulsesms -c /data/config.yaml
