window.addEventListener('DOMContentLoaded', () => {
  SwaggerUIBundle({
    url: '/swagger/openapi.json',
    dom_id: '#swagger-ui',
    validatorUrl: null,
    docExpansion: 'list',
    defaultModelsExpandDepth: -1,
    tryItOutEnabled: true,
    persistAuthorization: false
  });
});
