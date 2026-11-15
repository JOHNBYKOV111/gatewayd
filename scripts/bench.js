import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  vus: 2,             // 2 виртуальных пользователя (чтобы Ollama не захлебнулась)
  duration: '60s',    // 60 секунд нагрузки
};

const GW_KEY = __ENV.GW_KEY;

export default function () {
  const payload = JSON.stringify({
    model: 'llama3.2:3b', // <-- Ollama
    messages: [{ role: 'user', content: 'Say hi' }],
    stream: false,
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${GW_KEY}`,
    },
    timeout: '300s', // <-- Увеличенный таймаут для Ollama на CPU
  };

  const res = http.post('http://localhost:8080/v1/chat/completions', payload, params);

  check(res, {
    'status is 200': (r) => r.status === 200,
  });

  sleep(1); // <-- Пауза, чтобы Ollama успевала отвечать
}