# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: auth.spec.ts >> E2E-01: login y persistencia de sesión
- Location: e2e/auth.spec.ts:6:1

# Error details

```
Error: Falta WALLET_E2E_EMAIL

expect(received).toBeTruthy()

Received: undefined
```

# Test source

```ts
  1  | import { test, expect } from '@playwright/test';
  2  | 
  3  | const email = process.env.WALLET_E2E_EMAIL;
  4  | const password = process.env.WALLET_E2E_PASSWORD;
  5  | 
  6  | test('E2E-01: login y persistencia de sesión', async ({ page }) => {
> 7  |   expect(email, 'Falta WALLET_E2E_EMAIL').toBeTruthy();
     |                                           ^ Error: Falta WALLET_E2E_EMAIL
  8  |   expect(password, 'Falta WALLET_E2E_PASSWORD').toBeTruthy();
  9  | 
  10 |   // Abrir la pantalla de acceso.
  11 |   await page.goto('/login');
  12 | 
  13 |   let emailField = page.locator(
  14 |     'input[type="email"], input[name*="email" i], input[autocomplete="username"]'
  15 |   ).first();
  16 | 
  17 |   if (!(await emailField.isVisible().catch(() => false))) {
  18 |     await page.goto('/');
  19 |     emailField = page.locator(
  20 |       'input[type="email"], input[name*="email" i], input[autocomplete="username"]'
  21 |     ).first();
  22 |   }
  23 | 
  24 |   await expect(emailField).toBeVisible();
  25 |   const passwordField = page.locator('input[type="password"]').first();
  26 |   await expect(passwordField).toBeVisible();
  27 | 
  28 |   await emailField.fill(email!);
  29 |   await passwordField.fill(password!);
  30 | 
  31 |   const loginButton = page.getByRole('button', {
  32 |     name: /iniciar sesión|iniciar sesion|entrar|acceder|login|sign in/i
  33 |   }).first();
  34 | 
  35 |   await expect(loginButton).toBeVisible();
  36 |   await loginButton.click();
  37 | 
  38 |   // Diagnosticar la respuesta real del login si no se autentica.
  39 |   try {
  40 |     await expect.poll(async () => {
  41 |       const passwordVisible = await page.locator(
  42 |         'input[type="password"]:visible'
  43 |       ).count();
  44 | 
  45 |       const path = new URL(page.url()).pathname;
  46 |       return passwordVisible === 0 || !/\/login\/?$/.test(path);
  47 |     }, { timeout: 20000 }).toBe(true);
  48 |   } catch (error) {
  49 |     console.log('URL tras login:', page.url());
  50 |     console.log('Texto visible:', (await page.locator('body').innerText()).slice(0, 5000));
  51 |     console.log('Campos visibles:', await page.locator('input:visible').evaluateAll(
  52 |       els => els.map(e => ({
  53 |         type: (e as HTMLInputElement).type,
  54 |         name: (e as HTMLInputElement).name,
  55 |         placeholder: (e as HTMLInputElement).placeholder
  56 |       }))
  57 |     ));
  58 |     throw error;
  59 |   }
  60 | 
  61 |   // Recargar y comprobar que el acceso continúa activo.
  62 |   await page.reload();
  63 | 
  64 |   await expect.poll(async () => {
  65 |     const passwordVisible = await page.locator(
  66 |       'input[type="password"]:visible'
  67 |     ).count();
  68 | 
  69 |     const path = new URL(page.url()).pathname;
  70 |     return passwordVisible === 0 || !/\/login\/?$/.test(path);
  71 |   }, { timeout: 15000 }).toBe(true);
  72 | 
  73 |   console.log('E2E-01 PASS: autenticación y sesión persistente');
  74 | });
  75 | 
```