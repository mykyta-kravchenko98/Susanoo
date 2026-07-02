resource "aws_secretsmanager_secret" "telegram_bot_token" {
  name = "${var.project_name}/telegram-bot-token"
}

resource "aws_secretsmanager_secret" "anthropic_api_key" {
  name = "${var.project_name}/anthropic-api-key"
}

# После apply залить значения так (пример, выполнить один раз локально):
#
#   aws secretsmanager put-secret-value \
#     --secret-id susanoo/telegram-bot-token \
#     --secret-string "1234567:AAExampleTokenFromBotFather" \
#     --profile telegram-archiver --region eu-central-1
#
#   aws secretsmanager put-secret-value \
#     --secret-id susanoo/anthropic-api-key \
#     --secret-string "sk-ant-..." \
#     --profile telegram-archiver --region eu-central-1