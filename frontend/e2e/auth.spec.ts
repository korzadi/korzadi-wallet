import { test, expect } from '@playwright/test';

const email = process.env.WALLET_E2E_EMAIL;
const password = process.env.WALLET_E2E_PASSWORD;

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
