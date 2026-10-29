REGLAS GLOBALES OBLIGATORIAS DE KORZADI

Auditoría integral, seguridad, integridad funcional y corrección de extremo a extremo

1. ALCANCE GLOBAL DEL PROYECTO

Estas reglas se aplican a todo el repositorio de Korzadi, sin excepción por directorio, archivo, módulo, servicio, pantalla o funcionalidad.

El alcance incluye, entre otros:

- "backend/": dominio, aplicación, infraestructura, HTTP, handlers, servicios, repositorios, middleware y configuración.
- "frontend/": páginas, componentes, formularios, navegación, estado, hooks, estilos, temas, clientes API y recursos públicos.
- "database/": esquema de referencia, migraciones, integridad, índices y relaciones.
- "infrastructure/": configuración y recursos de infraestructura.
- "docs/": arquitectura, contratos API, seguridad y documentación funcional.
- "docker-compose.yml", "docker-compose.test.yml" y Dockerfiles.
- Configuración de producción, PWA, políticas de seguridad y proxy inverso, cuando corresponda.
- Integraciones externas, proveedores, redes blockchain, custodia, autenticación, KYC/AML y operaciones financieras.
- Herramientas, scripts, dependencias, pruebas y demás componentes existentes del repositorio.

La enumeración anterior no limita el alcance. Si existe un componente adicional dentro del proyecto, también debe considerarse cuando sea relevante.

No concentres la auditoría en un archivo determinado. Sigue cada funcionalidad a través de todos los componentes relacionados y evalúa el sistema completo.

2. OBJETIVO OBLIGATORIO

Ante cualquier tarea de desarrollo, corrección, mantenimiento o auditoría:

1. Identifica los problemas que puedan afectar a la funcionalidad, seguridad, integridad, rendimiento o coherencia del proyecto.
2. Investiga la causa raíz antes de modificar el código.
3. Localiza todos los componentes afectados, tanto directos como indirectos.
4. Corrige los problemas que puedan resolverse de forma segura y dentro del alcance autorizado.
5. Integra correctamente las correcciones entre backend, frontend, persistencia y servicios relacionados.
6. Ejecuta verificaciones pertinentes y documenta los resultados reales.
7. Registra los defectos pendientes y las limitaciones de verificación.

No te limites a enumerar problemas ni a proponer soluciones. Implementa las correcciones seguras en el código existente.

No conviertas cada tarea en una refactorización general. Prioriza cambios mínimos, precisos, completos y compatibles con la arquitectura actual.

3. INSPECCIÓN ANTES DE MODIFICAR

Antes de intervenir:

- Lee las instrucciones aplicables de "AGENTS.md", "SECURITY_REVIEW.md" y la documentación relevante.
- Identifica la arquitectura, las convenciones y los componentes existentes.
- Consulta el grafo de dependencias con Graphify cuando esté disponible y sea pertinente.
- Examina los archivos reales y sus relaciones antes de asumir nombres, rutas, contratos o comportamientos.
- Inspecciona los cambios preexistentes del usuario para evitar sobrescribirlos.
- Localiza las implementaciones, pruebas y mecanismos reutilizables.
- Comprueba si el problema ya está parcialmente resuelto en otra capa.

No edites un archivo basándote únicamente en su nombre, un fragmento aislado o una suposición.

No reemplaces archivos completos cuando una modificación localizada sea suficiente.

4. AUDITORÍA INTEGRAL DE FUNCIONALIDAD

Examina las funciones del proyecto desde su origen hasta su resultado final.

Para cada funcionalidad relevante, verifica las capas que le correspondan:

- Interfaz y controles de usuario.
- Cliente API y construcción de solicitudes.
- Rutas y middleware HTTP.
- Autenticación, autorización y validación.
- Handlers, servicios y casos de uso.
- Dominio y reglas de negocio.
- Repositorios y persistencia.
- Transacciones, restricciones y migraciones.
- Servicios externos y tareas en segundo plano.
- Respuestas del servidor y representación final en la interfaz.
- Registros, auditoría y tratamiento de errores.

Busca errores lógicos, excepciones no controladas, datos incorrectos, validaciones ausentes, estados imposibles, condiciones de carrera, inconsistencias, código inalcanzable, dependencias innecesarias y fallos de integración.

Comprueba también el comportamiento ante datos ausentes, solicitudes repetidas, interrupciones de servicios, respuestas inesperadas y permisos insuficientes.

No todas las funcionalidades requieren las mismas capas: identifica las que correspondan según la arquitectura real, sin exigir persistencia artificial a funciones que no la necesitan.

5. INTEGRIDAD GLOBAL ENTRE BACKEND Y FRONTEND

La coherencia entre backend y frontend es obligatoria para todo el proyecto, no solo para una pantalla o módulo.

5.1 Desde el frontend hacia el backend

Cada funcionalidad visible debe tener una implementación real y apropiada cuando requiera operaciones del servidor.

Verifica que:

- Los botones, formularios, enlaces y controles ejecuten la acción anunciada.
- Los endpoints, métodos HTTP, parámetros, encabezados y cuerpos coincidan con el contrato real.
- Los campos, tipos, formatos, enumeraciones y estados sean compatibles.
- La autenticación y los permisos estén aplicados correctamente.
- Los resultados se muestren únicamente después de recibir la respuesta correspondiente.
- Los errores se comuniquen correctamente y no se transformen en falsos éxitos.
- No se utilicen respuestas simuladas, datos ficticios o estados visuales engañosos para aparentar funcionalidad real.

5.2 Desde el backend hacia el frontend

Revisa las funcionalidades existentes del backend e identifica cuáles deben estar disponibles en la interfaz según el alcance del producto.

Comprueba que:

- Los endpoints pertinentes estén conectados a los componentes correspondientes.
- Los contratos estén documentados o sean verificables en el código.
- Los permisos, estados y errores se representen correctamente.
- No existan rutas relevantes desconectadas, integraciones incompletas o respuestas que el frontend interprete incorrectamente.
- No haya implementaciones duplicadas que provoquen comportamientos inconsistentes.

No todos los endpoints necesitan una pantalla: pueden existir endpoints internos, de salud, webhooks o de infraestructura. Evalúa su finalidad antes de clasificarlos como desconectados.

5.3 Regla de corrección

Cuando falte una implementación necesaria, desarrolla la solución en las capas correspondientes siguiendo la arquitectura existente.

Si falta una API, implementa la ruta, validación, autorización, lógica y persistencia que realmente correspondan antes de conectar el frontend.

Si la API ya existe, reutilízala y corrige la integración sin duplicarla.

No elimines una funcionalidad visible para evitar implementar su respaldo real. Tampoco inventes endpoints, capacidades o datos sin comprobar primero las necesidades y los mecanismos existentes.

6. AUDITORÍA DE SEGURIDAD EN TODO EL SISTEMA

Inspecciona las vulnerabilidades aplicables a cada componente y a sus interacciones, incluyendo:

- Autenticación, autorización, sesiones, JWT y recuperación de cuentas.
- Acceso horizontal o vertical no autorizado, IDOR/BOLA y escalada de privilegios.
- Protección de operaciones administrativas y autenticación de dos factores.
- Inyecciones SQL, XSS, CSRF, SSRF y manejo inseguro de archivos.
- Validación de entradas y salidas, serialización y deserialización.
- CORS, CSP, cookies y encabezados de seguridad.
- Limitación de solicitudes, fuerza bruta y abuso de endpoints.
- Secretos, claves, tokens y datos sensibles expuestos.
- Dependencias vulnerables y configuraciones inseguras.
- Webhooks, integraciones externas, RPC y servicios de terceros.
- Registros de auditoría, errores y exposición de información.
- Seguridad de la base de datos, infraestructura y comunicaciones.

Verifica la aplicación efectiva de cada control, no solo su existencia nominal.

Evalúa las vulnerabilidades en función de su impacto, explotabilidad, exposición y consecuencias. No declares un componente seguro únicamente porque no hayas encontrado fallos durante una revisión limitada.

No imprimas secretos completos ni los copies a informes, archivos temporales o salidas de diagnóstico.

7. INTEGRIDAD FINANCIERA Y BLOCKCHAIN

Las operaciones financieras y de custodia requieren un nivel de protección reforzado.

Audita los flujos aplicables de:

- Saldos y movimientos contables.
- Contabilidad de doble entrada y ledger.
- Depósitos, transferencias y retiros.
- Comisiones, precisión decimal y límites.
- Redes blockchain, confirmaciones y direcciones.
- Custodia y gestión de claves.
- Bloqueos de saldo, escrow y órdenes P2P.
- Disputas, liberaciones y acciones administrativas.
- Idempotencia, atomicidad, concurrencia y prevención del doble gasto.

Verifica las reglas de negocio y las transiciones de estado de extremo a extremo.

Nunca modifiques saldos reales, fondos, claves de custodia o movimientos financieros para facilitar una prueba. No ejecutes operaciones reales durante la auditoría.

No confirmes depósitos basándote exclusivamente en datos enviados por el frontend, comprobantes o respuestas no verificadas.

No desactives controles financieros o de seguridad para conseguir que las pruebas pasen.

8. DATOS, FUNCIONES Y CONFIGURACIONES FALTANTES

Busca datos ausentes, campos sin origen, contratos incompletos, validaciones omitidas, integraciones desconectadas y configuraciones necesarias que no estén disponibles.

Para cada ausencia:

1. Busca primero la fuente real dentro del proyecto y las integraciones existentes.
2. Comprueba si la información ya existe con otro nombre o en otra capa.
3. Reutiliza la fuente correcta y evita duplicidades.
4. Implementa los componentes faltantes cuando corresponda y sea seguro.
5. Verifica su conexión con los componentes consumidores.
6. Si falta una decisión del propietario, una credencial externa o un dato no verificable, documenta el bloqueo y no inventes su valor.

No sustituyas datos reales por valores ficticios ni presentes como confirmados datos que todavía no se hayan verificado.

9. CONSERVAR EL DISEÑO Y LA IDENTIDAD DEL PROYECTO

Todas las correcciones del frontend deben respetar el sistema visual existente de Korzadi.

Antes de modificar la interfaz, inspecciona los componentes reutilizables, estilos, tokens, iconos, tipografía, espaciados, navegación y sistema de temas.

Es obligatorio:

- Mantener la identidad visual y el diseño existente.
- Reutilizar los componentes y patrones ya implementados.
- Conservar las funciones y secciones que operan correctamente.
- Mantener la compatibilidad con los temas claro, oscuro y automático, cuando estén implementados.
- Preservar el comportamiento responsive en móviles, tablets y ordenadores.
- Corregir problemas de accesibilidad, usabilidad y visualización sin rediseños innecesarios.
- Mantener coherencia entre todas las páginas y módulos.
- Evitar estilos paralelos, componentes duplicados y dependencias innecesarias.

No sustituyas el diseño existente por plantillas genéricas. La mejora visual nunca debe ocultar errores funcionales ni degradar la seguridad.

10. DEPENDENCIAS, SKILLS Y HERRAMIENTAS

Antes de ejecutar una tarea:

1. Identifica las especialidades necesarias.
2. Consulta las Skills disponibles en ".agents/skills/".
3. Lee el "SKILL.md" de cada Skill pertinente.
4. Combina las instrucciones aplicables sin duplicar trabajo.
5. Utiliza las herramientas existentes, incluidos Graphify y las pruebas del proyecto, cuando sean relevantes.
6. Instala herramientas o dependencias únicamente cuando exista una necesidad técnica justificada y se respeten las autorizaciones del proyecto.

Prioriza las Skills pertinentes a Go, React, seguridad, arquitectura, PostgreSQL, Redis, blockchain, operaciones financieras, accesibilidad, diagnóstico y pruebas.

No afirmes haber utilizado una Skill o herramienta cuyo contenido o ejecución no hayas comprobado.

No instales paquetes, actualices versiones ni cambies la infraestructura sin la autorización requerida.

11. PRIORIZACIÓN Y EJECUCIÓN DE CORRECCIONES

Clasifica los hallazgos por gravedad:

- Crítico: riesgo inmediato para fondos, custodia, datos sensibles, acceso privilegiado o integridad del sistema.
- Alto: vulnerabilidades explotables, fallos graves de autorización o errores importantes en operaciones.
- Medio: fallos funcionales, inconsistencias, validaciones insuficientes o problemas con impacto limitado.
- Bajo: defectos menores, accesibilidad, mantenibilidad o detalles visuales.

Investiga la causa raíz y corrige primero los problemas de mayor impacto que puedan resolverse de forma segura.

Aplica cambios mínimos y coherentes en todas las capas afectadas. Comprueba los efectos colaterales antes de dar una corrección por terminada.

Si una operación requiere autorización, no la ejecutes. Continúa con los trabajos seguros y deja explícitamente documentado el bloqueo.

12. PRUEBAS Y VERIFICACIÓN

Utiliza primero las herramientas y pruebas existentes.

Ejecuta solo comprobaciones locales y no destructivas apropiadas para los cambios realizados, por ejemplo:

- Compilación focalizada del backend o frontend.
- Análisis estático y comprobación de tipos.
- Pruebas unitarias relevantes.
- Validación de rutas y contratos API.
- Verificación de permisos y validaciones mediante entornos seguros.
- Pruebas de integración no destructivas.
- Revisión de migraciones sin aplicarlas sobre datos reales.
- Verificación de estilos y comportamiento responsive cuando existan medios seguros para hacerlo.

No ejecutes pruebas que envíen dinero, modifiquen usuarios reales, cambien contraseñas reales, activen transferencias o alteren datos financieros.

No afirmes que un flujo está probado de extremo a extremo si únicamente has inspeccionado su código.

Distingue siempre entre código inspeccionado, corrección implementada, prueba ejecutada y funcionamiento verificado en runtime.

13. PROHIBICIONES OPERATIVAS GLOBALES

Sin autorización explícita, queda prohibido:

- Reiniciar Docker, contenedores, servicios o procesos de producción.
- Reconstruir imágenes o desplegar cambios.
- Modificar ".env", secretos, credenciales, claves API o claves privadas.
- Borrar datos, reinicializar bases de datos o ejecutar migraciones destructivas.
- Alterar saldos, movimientos, fondos, claves de custodia o redes operativas.
- Crear copias de seguridad adicionales.
- Modificar permisos del sistema, firewall, red o configuración de Nginx.
- Descartar cambios preexistentes del usuario.
- Ejecutar operaciones Git destructivas.
- Hacer commits, pushes o publicaciones.
- Instalar dependencias o cambiar versiones cuando requiera autorización.
- Ejecutar pruebas destructivas o contra servicios reales.
- Ampliar la tarea a una refactorización general sin necesidad justificada.

No interpretes la autorización para editar código como autorización para desplegarlo o modificar el entorno de producción.

14. REGISTRO GLOBAL EN "SECURITY_REVIEW.md"

"SECURITY_REVIEW.md" es el registro operativo de los hallazgos de seguridad, integridad y funcionalidad identificados durante las auditorías del proyecto.

Su alcance comprende todo Korzadi, no un archivo o módulo específico.

Debe reflejar el estado real de los problemas:

- Mantener los hallazgos todavía pendientes.
- Registrar su gravedad, alcance, causa raíz y solución prevista.
- Actualizar su estado cuando una corrección haya sido implementada.
- Eliminar de la lista de pendientes los hallazgos cuya resolución haya sido verificada.
- Conservar una referencia a la evidencia o verificación pertinente cuando sea útil.
- No declarar resuelto un problema por el simple hecho de modificar el código.
- No eliminar hallazgos que continúen parcialmente pendientes.

El documento debe mostrar únicamente los problemas que aún necesitan atención, junto con el contexto mínimo necesario para resolverlos y verificar su estado.

No borres hallazgos pendientes ni elimines su información antes de comprobar que la corrección funciona.

15. INFORME FINAL OBLIGATORIO

Al terminar una auditoría o un grupo de correcciones, informa de forma concreta:

1. Alcance revisado y áreas que no pudieron inspeccionarse.
2. Hallazgos identificados y gravedad.
3. Causas raíz y consecuencias.
4. Archivos modificados y motivo de cada cambio.
5. Correcciones aplicadas en backend, frontend y demás capas afectadas.
6. Endpoints, contratos, permisos y flujos verificados.
7. Datos o funcionalidades que faltaban y cómo se resolvieron.
8. Skills y herramientas utilizadas realmente.
9. Pruebas ejecutadas y resultados reales.
10. Verificaciones que faltan y cómo realizarlas de forma segura.
11. Riesgos residuales, bloqueos y decisiones que requieren autorización.
12. Estado actualizado de "SECURITY_REVIEW.md".

Indica expresamente qué quedó revisado, corregido, probado, verificado en runtime o pendiente.

Confirma si no se reiniciaron servicios, no se reconstruyeron imágenes, no se desplegaron cambios, no se modificaron secretos y no se alteraron datos financieros.

No inventes resultados, no ocultes fallos y no afirmes que el proyecto está completamente seguro si la cobertura de auditoría no lo demuestra.

16. REGLA FINAL DE APLICACIÓN UNIVERSAL

Estas instrucciones prevalecen para todo el proyecto y deben aplicarse a cualquier tarea que pueda afectar a su seguridad, funcionamiento, integridad, diseño o mantenimiento.

Ningún archivo, pantalla, endpoint, servicio o módulo queda excluido por no estar mencionado expresamente.

Inspecciona el contexto completo de cada cambio, identifica todas las capas afectadas, corrige las causas raíz, conecta correctamente los componentes y verifica los resultados.

No elimines funciones para evitar implementarlas. No simules datos reales. No ocultes errores. No rompas el diseño existente. No realices operaciones protegidas sin autorización.

El objetivo es mantener Korzadi coherente, funcional, seguro, verificable y fiel a su arquitectura y diseño actuales, en todo el backend, frontend e infraestructura relacionada.
