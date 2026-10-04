// The setup page of the Android and iOS apps: asks for the knowpod server's address and hands it to
// the app (SetupPlugin.java, SetupPlugin.swift), like the desktop app's (desktop/src/setup.js).
const form = document.getElementById('form');
const input = document.getElementById('url');
const error = document.getElementById('error');
const cancel = document.getElementById('cancel');

const call = (method, options = {}) => window.Capacitor.nativePromise('KnowpodSetup', method, options);

function showError(text) {
  error.textContent = text;
  error.hidden = !text;
}

call('get').then(({ current, error: failed }) => {
  if (current) {
    input.value = current;
    cancel.hidden = false;
    cancel.addEventListener('click', () => call('cancel'));
  }
  if (failed) showError(`Couldn't connect: ${failed}`);
});

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  showError('');
  const result = await call('setServer', { url: input.value });
  if (!result.ok) showError('Enter an address like https://knowpod.example.com.');
});
