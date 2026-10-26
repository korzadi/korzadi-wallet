import { test, expect } from '@playwright/test';
import { createHmac } from 'node:crypto';

const email = process.env.WALLET_E2E_EMAIL;
const password = process.env.WALLET_E2E_PASSWORD;
const totpSecret = process.env.WALLET_E2E_TOTP_SECRET;

function decodeBase32(input: string): Buffer {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  const clean = input.toUpperCase().replace(/[^A-Z2-7]/g, '');
  const bytes: number[] = [];
  let bits = 0;
  let value = 0;
  for (const char of clean) {
    value = (value << 5) | alphabet.indexOf(char);
    bits += 5;
    if (bits >= 8) {
      bytes.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(bytes);
}

function generateTotp(secret: string, at = Date.now()): string {
  const counter = Buffer.alloc(8);
  counter.writeUInt32BE(Math.floor(at / 1000 / 30), 4);
  const digest = createHmac('sha1', decodeBase32(secret)).update(counter).digest();
  const offset = digest[digest.length - 1] & 0x0f;
  const value = (digest.readUInt32BE(offset) & 0x7fffffff) % 1_000_000;
  return String(value).padStart(6, '0');
}

test('E2E-01: login y persistencia de sesión', async ({ page }) => {
  expect(email, 'Falta WALLET_E2E_EMAIL').toBeTruthy();
  expect(password, 'Falta WALLET_E2E_PASSWORD').toBeTruthy();

  // Abrir la pantalla de acceso.
  await page.goto('/login');

  let emailField = page.locator(
    'input[type="email"], input[name*="email" i], input[autocomplete="username"]'
  ).first();

  if (!(await emailField.isVisible().catch(() => false))) {
    await page.goto('/');
    emailField = page.locator(
      'input[type="email"], input[name*="email" i], input[autocomplete="username"]'
    ).first();
  }

  await expect(emailField).toBeVisible();
  const passwordField = page.locator('input[type="password"]').first();
  await expect(passwordField).toBeVisible();

  await emailField.fill(email!);
  await passwordField.fill(password!);

  const loginButton = page.getByRole('button', {
    name: /iniciar sesión|iniciar sesion|entrar|acceder|login|sign in/i
  }).first();

  await expect(loginButton).toBeVisible();
  await loginButton.click();

  // Si la cuenta tiene 2FA, el backend no entrega sesión hasta verificar el
  // segundo factor. El secreto TOTP real se toma del entorno de pruebas.
  const twoFaInput = page.locator('input[placeholder="000000"]').first();
  const requires2FA = await twoFaInput
    .waitFor({ state: 'visible', timeout: 5000 })
    .then(() => true)
    .catch(() => false);
  if (requires2FA) {
    expect(totpSecret, 'Falta WALLET_E2E_TOTP_SECRET para la cuenta con 2FA').toBeTruthy();
    await twoFaInput.fill(generateTotp(totpSecret!));
    await page.getByRole('button', { name: /^verificar$/i }).first().click();
  }

  // Diagnosticar la respuesta real del login si no se autentica.
  try {
    await expect.poll(async () => {
      const passwordVisible = await page.locator(
        'input[type="password"]:visible'
      ).count();

      const path = new URL(page.url()).pathname;
      return passwordVisible === 0 || !/\/login\/?$/.test(path);
    }, { timeout: 20000 }).toBe(true);
  } catch (error) {
    console.log('URL tras login:', page.url());
    console.log('Texto visible:', (await page.locator('body').innerText()).slice(0, 5000));
    console.log('Campos visibles:', await page.locator('input:visible').evaluateAll(
      els => els.map(e => ({
        type: (e as HTMLInputElement).type,
        name: (e as HTMLInputElement).name,
        placeholder: (e as HTMLInputElement).placeholder
      }))
    ));
    throw error;
  }

  // Recargar y comprobar que el acceso continúa activo.
  await page.reload();

  await expect.poll(async () => {
    const passwordVisible = await page.locator(
      'input[type="password"]:visible'
    ).count();

    const path = new URL(page.url()).pathname;
    return passwordVisible === 0 || !/\/login\/?$/.test(path);
  }, { timeout: 15000 }).toBe(true);

  console.log('E2E-01 PASS: autenticación y sesión persistente');
});
