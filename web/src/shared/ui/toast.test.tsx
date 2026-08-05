import { act, fireEvent, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ToastProvider, useToast } from './toast'

function Trigger({ message = 'Copied' }: { message?: string }) {
  const toast = useToast()
  return <button onClick={() => toast(message)}>say it</button>
}

describe('the toast', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  const press = () => {
    const button = screen.getByRole('button', { name: 'say it' })
    button.focus()
    fireEvent.click(button)
  }

  it('says what happened, then goes away on its own', async () => {
    render(
      <ToastProvider>
        <Trigger />
      </ToastProvider>,
    )
    expect(screen.queryByRole('status')).toBeNull()

    press()
    expect(screen.getByRole('status')).toHaveTextContent('Copied')

    // It cannot be dismissed, so leaving on its own is the only way out — a
    // toast that stayed would sit over the page for the rest of the session.
    act(() => void vi.advanceTimersByTime(3000))
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('is announced without taking focus', async () => {
    // The viewer is in the middle of something; the report is worth hearing and
    // never worth interrupting for.
    render(
      <ToastProvider>
        <Trigger />
      </ToastProvider>,
    )
    press()

    const status = screen.getByRole('status')
    expect(status).toHaveAttribute('aria-live', 'polite')
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'say it' }))
  })

  it('restarts the clock when the same thing is said twice', async () => {
    render(
      <ToastProvider>
        <Trigger />
      </ToastProvider>,
    )
    press()
    act(() => void vi.advanceTimersByTime(2000))
    press()

    // Pressing again with the line already up must not let the first press's
    // timer take the second one's message away almost immediately — which is
    // what a message-keyed toast would do.
    act(() => void vi.advanceTimersByTime(1000))
    expect(screen.getByRole('status')).toHaveTextContent('Copied')
  })

  it('does nothing where no provider is mounted', async () => {
    // Callers should not have to know whether they are inside the shell — a
    // missing provider must not be an exception thrown from a button.
    render(<Trigger />)
    press()
    expect(screen.queryByRole('status')).toBeNull()
  })
})
