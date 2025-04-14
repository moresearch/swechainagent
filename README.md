# SWEChain Agent

## Setup Ollama
export OLLAMA_NUM_PARALLEL=4
echo "Starting Ollama with OLLAMA_NUM_PARALLEL=$OLLAMA_NUM_PARALLEL"
nohup ollama serve > ollama.log 2>&1 &
