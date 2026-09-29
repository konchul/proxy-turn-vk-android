# 27 White (WDTT fork)

Оптимизированный форк [proxy-turn-vk-android](https://github.com/amurcanov/proxy-turn-vk-android):
VPN-клиент и сервер, маскирующие трафик под RTP-поток VK-звонков.

## Что добавлено относительно апстрима

- **Сервер: 4× пропускной способности** (~50k pps/ядро против ~12k), −50% CPU
- Zero-allocation крипто hot path: fastPRNG, стек-nonce, кольцевой anti-replay
- Батчинг системных вызовов (recvmmsg/sendmmsg) на raw-листенерах
- **Новый протокол AES-256-GCM** на отдельном порту (46000) — аппаратное
  шифрование, старые клиенты работают по-прежнему (ChaCha, 56000/56003)
- Самовосстановление после простоя: keepalive-понг + пересоздание мёртвых
  сессий (90с × 3)
- VNET_HDR/GSO-сегментация TUN (TCP), флаг `-tun-gso`
- Деплой с выбором портов из приложения; ядро embedded-сервера стрипнуто
- APK 18 МБ (реальный R8-релиз)

Подробности и бенчмарки: [docs/OPTIMIZATIONS.md](docs/OPTIMIZATIONS.md)

## Сборка

```bash
scripts/build-all.sh          # Go-библиотека + сервер
./gradlew assembleRelease     # APK (нужен SDK/NDK, local.properties)
```

Деплой сервера: из приложения (вкладка Deploy) или `app/src/main/assets/deploy.sh`.

## Лицензия

GPL-3.0 — наследуется от апстрима. Все изменения этого форка — под той же лицензией.
