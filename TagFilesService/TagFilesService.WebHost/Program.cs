using Microsoft.EntityFrameworkCore;
using Minio;
using TagFilesService.Infrastructure;
using TagFilesService.Model;
using TagFilesService.Thumbnail;
using TagFilesService.WebHost;
using TagFilesService.WebHost.Options;

WebApplicationBuilder builder = WebApplication.CreateBuilder(args);
builder.Services.AddControllers();
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen(x => { x.OperationFilter<FileUploadOperationFilter>(); });

builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlite("Data Source=tag-files.db"));

S3Option? s3Options = builder.Configuration.GetSection(nameof(S3Option)).Get<S3Option>();
builder.Services.AddMinio(configure =>
{
    if (s3Options == null)
    {
        throw new ArgumentNullException(nameof(s3Options));
    }

    configure
        .WithEndpoint(s3Options!.Host)
        .WithCredentials(s3Options.AccessKey, s3Options.SecretKey)
        .WithSSL(false)
        .Build();
});
builder.Services.AddScoped<IFileStorage, FileStorage>();
builder.Services.AddScoped<IThumbnailService, ThumbnailService>();
builder.Services.AddScoped<ITagsRepository, TagsRepository>();
builder.Services.AddScoped<IMetadataService, MetadataService>();

WebApplication app = builder.Build();
app.MapControllers();
app.UseSwagger();
app.UseSwaggerUI();

app.MapGet("/ping", () => "pong");

app.Run();