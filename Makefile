# Makefile

# Define variables for easier management
FEEDBACK_BIN = ./bin/feedback
ENV = bob
MODE = train
LLM = deepseek-r1:1.5b
TRAINSET = ./train_dataset/rag_examples.json

# Default target
all: run_feedback

# Target to run the feedback command
run_feedback:
	$(FEEDBACK_BIN) --env $(ENV) --mode $(MODE) --llm $(LLM) --trainset $(TRAINSET)

# Clean target (if needed, to clean up)
clean:
	rm -rf ./bin/*
