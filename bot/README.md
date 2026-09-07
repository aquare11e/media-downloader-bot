# Download Bot

A Telegram bot that facilitates the management of media downloads by interacting with the Coordinator service.

## Overview

This bot allows users to initiate downloads of media content by sending magnet links or torrent files. It communicates with the Coordinator service to manage the download process and provides real-time updates on the download status.

## Configuration

The bot requires the following environment variables to be set:

- `TELEGRAM_BOT_TOKEN`: The token for the Telegram bot.
- `ALLOWED_USERS`: A comma-separated list of usernames that are allowed to use the bot.
- `COORDINATOR_URL`: The URL of the Coordinator service.
- `REDIS_URL`: The URL of the Redis server.
- `REDIS_PASSWORD`: The password for the Redis server (optional).


## Building and Running

1. Set up environment variables:
   ```bash
   export TELEGRAM_BOT_TOKEN=your-telegram-bot-token
   export ALLOWED_USERS=user1,user2,user3
   export COORDINATOR_URL=your-coordinator-url
   export REDIS_URL=your-redis-url
   export REDIS_PASSWORD=your-redis-password  # Optional
   ```

2. Build and run the bot:
   ```bash
   go build -o download-bot ./internal/bot
   ./download-bot
   ```

## Commands

- `/start`: Initializes the bot and provides a welcome message.
- `/download`: Starts the download process. The user will be prompted to send a magnet link or a torrent file.
- `/status`: Provides the current status of ongoing downloads. The user can check the progress and any messages related to their download requests.
- `/help`: Provides a list of available commands and their descriptions.



## Download Process

1. **Start the Download**: The user sends the `/download` command.
2. **Send Magnet Link or Torrent File**: The bot prompts the user to send a magnet link or a torrent file.
3. **Select Category**: After receiving a valid input, the bot prompts the user to select a category for the download (Films, Series, Cartoons, Cartoon Series, Cartoon Shorts, or 🎮 Switch). The `🎮 Switch` category downloads into `SWITCH_DIR_PATH` and skips Plex entirely.
4. **Download Status Updates**: The bot communicates with the Coordinator service to start the download and provides real-time updates on the download progress.

## Security Considerations

- Ensure that the `TELEGRAM_BOT_TOKEN` is kept secure and not exposed in logs or error messages.
- Only allow trusted users to interact with the bot by specifying their usernames in the `ALLOWED_USERS` environment variable.

## Example Usage

To start using the bot, send the `/start` command in your Telegram chat. Follow the prompts to initiate a download.
