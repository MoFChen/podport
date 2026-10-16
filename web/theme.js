(function(){
 let theme='auto';try{theme=localStorage.getItem('podport-theme')||'auto'}catch{}
 if(!['auto','light','dark'].includes(theme))theme='auto';document.documentElement.dataset.theme=theme;
})();
