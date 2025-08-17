using System.Text.Json.Serialization;
using Microsoft.EntityFrameworkCore;
using Minio;
using TagFilesService.FilesProcessing;
using TagFilesService.FilesProcessing.Handlers;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

WebApplicationBuilder builder = WebApplication.CreateBuilder(args);
builder.Services.Configure<FfmpegOptions>(builder.Configuration.GetSection("Ffmpeg"));
builder.Services
    .AddControllers()
    .AddJsonOptions(options => { options.JsonSerializerOptions.Converters.Add(new JsonStringEnumConverter()); });
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen(options =>
{
    options.CustomSchemaIds(type =>
    {
        string name = type.Name;
        return name.EndsWith("Dto") ? name.Substring(0, name.Length - 3) : name;
    });
});
builder.Services.AddMediatR(cfg => cfg
    .RegisterServicesFromAssemblyContaining<AssignTagsHandler>()
    .RegisterServicesFromAssemblyContaining<FileProcessingHandler>());
builder.Services.AddCors(x => x.AddPolicy("AllowAll", policy =>
{
    policy
        .AllowAnyOrigin()
        .AllowAnyMethod()
        .AllowAnyHeader();
}));

builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlite(builder.Configuration.GetConnectionString("SQLite")));
builder.Services.AddMinio(configure => configure
    .WithEndpoint(builder.Configuration["S3:Url"])
    .WithCredentials(builder.Configuration["S3:Username"], builder.Configuration["S3:Password"])
    .WithSSL(false)
    .Build());
builder.Services.AddScoped<ITagsRepository, TagsRepository>();
builder.Services.AddScoped<ICategoriesRepository, CategoriesRepository>();

builder.Services.AddSingleton<FilesProcessingQueue>();
builder.Services.AddHostedService<BucketInitializer>();
builder.Services.AddHostedService<BucketWatcher>();
builder.Services.AddHostedService<FilesProcessingService>();

WebApplication app = builder.Build();
app.UseCors("AllowAll");
app.MapControllers();
app.UseSwagger();
app.UseSwaggerUI();

app.MapGet("/ping", () => "pong");

using (IServiceScope scope = app.Services.CreateScope())
{
    AppDbContext dbContext = scope.ServiceProvider.GetRequiredService<AppDbContext>();
    dbContext.Database.Migrate();
}

app.Run();