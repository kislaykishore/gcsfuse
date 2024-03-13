set -e
gcloud version
wget -O gcloud.tar.gz https://dl.google.com/dl/cloudsdk/channels/rapid/google-cloud-sdk.tar.gz -q
sudo rm -rf $(which gcloud) && sudo tar xzf gcloud.tar.gz && sudo mv google-cloud-sdk /usr/local
if [ ! -d $(which gcloud) ]; then
   sudo rm -rf $(which gcloud)
   echo "Here"
fi
sudo /usr/local/google-cloud-sdk/install.sh
sudo /usr/local/google-cloud-sdk/bin/gcloud components update
sudo /usr/local/google-cloud-sdk/bin/gcloud components install alpha
export PATH=$PATH:/usr/local/google-cloud-sdk/bin
echo 'export PATH=$PATH:/usr/local/google-cloud-sdk/bin' >> ~/.bashrc
gcloud version && rm gcloud.tar.gz
which gcloud
touch a.txt
gcloud alpha storage managed-folders create gs://write-test-gcsfuse-tulsishah/m888
sleep 100000
