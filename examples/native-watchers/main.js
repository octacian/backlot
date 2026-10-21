document.querySelector('#web').textContent = 'web-v1';
document.querySelector('#api').textContent = await (await fetch('/api/')).text();
if (import.meta.hot) import.meta.hot.accept();
