import { test, expect } from '@playwright/test';

const loginUrl = '**/api/auth/login';
const verifyUrl = '**/api/auth/login/2fa';

const fakeUser = {
  id: '00000000-0000-0000-0000-000000000001',
  email: 'persona@example.test',
  first_name: 'Persona',
  last_name: 'Prueba',
  status: 'active'
};

async function goToLogin(page: import('@playwright/test').Page) {
  await page.goto('/login');
  await page.evaluate(() => localStorage.clear());
  await page.reload();
}

test('no persiste tokens antes de verificar el segundo factor', async ({ page }) => {
  await page.route(loginUrl, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        two_factor_required: true,
        challenge_token: 'challenge-opaco-de-prueba',
        expires_at: new Date(Date.now() + 5 * 60_000).toISOString()
      })
    });
  });

  await goToLogin(page);
  await page.locator('input[type="email"]').first().fill('persona@example.test');
  await page.locator('input[type="password"]').first().fill('password-de-prueba');
  await page.getByRole('button', { name: /iniciar sesión|entrar|acceder/i }).first().click();

  await expect(page.getByRole('heading', { name: /verificación en 2 pasos/i })).toBeVisible();

  const storage = await page.evaluate(() => ({
    access: localStorage.getItem('korzadi_access_token'),
    refresh: localStorage.getItem('korzadi_refresh_token'),
    user: localStorage.getItem('korzadi_user'),
    challenge: localStorage.getItem('challenge_token')
  }));
  expect(storage.access).toBeNull();
  expect(storage.refresh).toBeNull();
  expect(storage.user).toBeNull();
  expect(storage.challenge).toBeNull();
  await expect(page).toHaveURL(/\/login\/?$/);
});

test('envía el challenge a /api/auth/login/2fa y solo entonces crea la sesión', async ({ page }) => {
  let verifyPayload: { challenge_token?: string; code?: string } = {};

  await page.route(loginUrl, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        two_factor_required: true,
        challenge_token: 'challenge-opaco-de-prueba',
        expires_at: new Date(Date.now() + 5 * 60_000).toISOString()
      })
    });
  });

  await page.route(verifyUrl, async (route) => {
    verifyPayload = route.request().postDataJSON();
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: fakeUser,
        access_token: 'eyJhbGciOiJIUzI1NiJ9.eyJ1aWQiOiJ4In0.signature',
        refresh_token: 'refresh-token-de-prueba',
        expires_at: new Date(Date.now() + 60_000).toISOString()
      })
    });
  });

  await goToLogin(page);
  await page.locator('input[type="email"]').first().fill('persona@example.test');
  await page.locator('input[type="password"]').first().fill('password-de-prueba');
  await page.getByRole('button', { name: /iniciar sesión|entrar|acceder/i }).first().click();

  await expect(page.getByRole('heading', { name: /verificación en 2 pasos/i })).toBeVisible();
  await page.locator('input[placeholder="000000"]').fill('123456');
  await page.getByRole('button', { name: /^verificar$/i }).click();

  await expect.poll(() => verifyPayload).toEqual({
    challenge_token: 'challenge-opaco-de-prueba',
    code: '123456'
  });
  await expect.poll(() => new URL(page.url()).pathname).toBe('/dashboard');

  const storage = await page.evaluate(() => ({
    access: localStorage.getItem('korzadi_access_token'),
    refresh: localStorage.getItem('korzadi_refresh_token')
  }));
  expect(storage.access).toBe('eyJhbGciOiJIUzI1NiJ9.eyJ1aWQiOiJ4In0.signature');
  expect(storage.refresh).toBe('refresh-token-de-prueba');
});


test('landing estática: el challenge no crea sesión antes del segundo factor', async ({ page }) => {
  let verifyPayload: { challenge_token?: string; code?: string } = {};

  await page.route(loginUrl, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        two_factor_required: true,
        challenge_token: 'challenge-landing',
        expires_at: new Date(Date.now() + 5 * 60_000).toISOString()
      })
    });
  });
  await page.route(verifyUrl, async (route) => {
    verifyPayload = route.request().postDataJSON();
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: fakeUser,
        access_token: 'access-token-landing',
        refresh_token: 'refresh-token-landing',
        expires_at: new Date(Date.now() + 60_000).toISOString()
      })
    });
  });

  await page.goto('/korzadi-landing.html');
  await page.evaluate(() => localStorage.clear());
  await page.getByRole('button', { name: /^iniciar sesión$/i }).first().click();
  await page.locator('#e').fill('persona@example.test');
  await page.locator('#p').fill('password-de-prueba');
  await page.locator('#sb').click();

  await expect(page.locator('#s2')).toBeVisible();
  const pending = await page.evaluate(() => ({
    access: localStorage.getItem('korzadi_access_token'),
    refresh: localStorage.getItem('korzadi_refresh_token')
  }));
  expect(pending.access).toBeNull();
  expect(pending.refresh).toBeNull();

  const digits = page.locator('#cd input');
  for (let i = 0; i < 6; i++) await digits.nth(i).fill('123456'[i]);
  await page.getByRole('button', { name: /^verificar$/i }).click();

  await expect.poll(() => verifyPayload).toEqual({
    challenge_token: 'challenge-landing',
    code: '123456'
  });
  await expect(page.locator('#s3')).toBeVisible();
  const storage = await page.evaluate(() => ({
    access: localStorage.getItem('korzadi_access_token'),
    refresh: localStorage.getItem('korzadi_refresh_token')
  }));
  expect(storage.access).toBe('access-token-landing');
  expect(storage.refresh).toBe('refresh-token-landing');
});
