using TagFilesService.Infrastructure;
using TagFilesService.Model;

WebApplicationBuilder builder = WebApplication.CreateBuilder(args);
builder.Services.AddControllers();
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen();

// TODO: Add SQLite DbContext
builder.Services.AddScoped<ITagsRepository, TagsRepository>();
builder.Services.AddScoped<IMetadataService, MetadataService>();

WebApplication app = builder.Build();
app.MapControllers();
app.UseSwagger();
app.UseSwaggerUI();

app.MapGet("/ping", () => "pong");

app.Run();