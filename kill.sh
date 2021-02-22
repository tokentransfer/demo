ps aux | grep demo | grep -v "grep" | awk '{print $2}' | xargs -r kill -9
