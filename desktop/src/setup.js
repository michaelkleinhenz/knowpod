// The setup page: asks for the knowpod server's address and hands it to the main process.
const params = new URLSearchParams(location.search);
const form = document.getElementById('form');
const input = document.getElementById('url');
const error = document.getElementById('error');
const cancel = document.getElementById('cancel');

const current = params.get('current');
if (current) {
  input.value = current;
  cancel.hidden = false;
  cancel.addEventListener('click', () => location.assign(current));
}

function showError(text) {
  error.textContent = text;
  error.hidden = !text;
}

if (params.get('error')) showError(`Couldn't connect: ${params.get('error')}`);

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  showError('');
  const result = await window.knowpodDesktop.setServer(input.value);
  if (!result.ok) showError('Enter an address like https://knowpod.example.com.');
});
