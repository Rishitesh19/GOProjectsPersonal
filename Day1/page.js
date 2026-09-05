const form = document.querySelector('#send');
const button = document.querySelector('#submit');
const status = document.querySelector('#status');
form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const file = document.querySelector('#file').files[0];
  const code = document.querySelector('#code').value.trim();
  if (!file || file.size > 50 * 1024 * 1024) {
    status.textContent = 'Choose one file up to 50 MiB.';
    return;
  }
  button.disabled = true;
  try {
    status.textContent = 'Waiting for approval in the Mac terminal. File contents have not been sent.';
    const approval = await fetch('/request', {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${code}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: file.name, size: file.size })
    });
    if (!approval.ok) throw new Error(await approval.text());
    const { ticket } = await approval.json();
    status.textContent = 'Approved. Sending file—keep this page open…';
    const result = await fetch('/upload', {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${code}`, 'X-Upload-Ticket': ticket },
      body: file
    });
    if (!result.ok) throw new Error(await result.text());
    status.textContent = await result.text();
    document.querySelector('#file').value = '';
  } catch (error) {
    status.textContent = error instanceof TypeError
      ? 'Connection lost or session ended. Check the Mac terminal before trying again.'
      : error.message;
  } finally {
    button.disabled = false;
  }
});
