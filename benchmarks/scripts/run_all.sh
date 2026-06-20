rm ../results/*

./setup.sh
./run_startup.sh
./run_cold.sh
./run_batch.sh
./run_micro.sh

python report.py