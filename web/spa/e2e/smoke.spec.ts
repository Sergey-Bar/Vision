import { test, expect } from '@playwright/test'

test('console loads and navigates between pages', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByText('Vision VMS').first()).toBeVisible()
  await expect(page.getByRole('link', { name: 'Live' })).toBeVisible()

  await page.getByRole('link', { name: 'Events' }).click()
  await expect(page.getByRole('heading', { name: 'Events' })).toBeVisible()

  await page.getByRole('link', { name: 'Incidents' }).click()
  await expect(page.getByRole('heading', { name: 'Incidents' })).toBeVisible()

  await page.getByRole('link', { name: 'Live' }).click()
  await expect(page.getByText(/Entrance North/)).toBeVisible()
})
